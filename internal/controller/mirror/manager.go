package mirror

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"

	crdv1alpha1 "github.com/christensenjairus/Multicluster-Failover-Operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	kubeconfigprovider "sigs.k8s.io/multicluster-runtime/providers/kubeconfig"
)

const (
	// Finalizer name for mirroring
	MirrorFinalizer = "mirror.crd.hahomelabs.com"

	// Maximum number of retries for sync operations
	maxRetries = 5

	// Initial retry delay
	initialRetryDelay = time.Second

	// Maximum retry delay
	maxRetryDelay = 5 * time.Minute

	// Batch size for status syncs
	statusSyncBatchSize = 10
)

// MirrorReconciler reconciles mirrored resources across clusters
type MirrorReconciler struct {
	client.Client
	Manager  mcmanager.Manager
	Provider *kubeconfigprovider.Provider
	Log      logr.Logger

	// Workqueues for batching status syncs
	statusSyncQueues map[string]*workqueue.RateLimitingInterface
}

// NewMirrorReconciler creates a new MirrorReconciler
func NewMirrorReconciler(mgr mcmanager.Manager) *MirrorReconciler {
	return &MirrorReconciler{
		Manager:          mgr,
		Provider:         mgr.GetProvider().(*kubeconfigprovider.Provider),
		Log:              log.FromContext(context.Background()).WithName("mirror-reconciler"),
		statusSyncQueues: make(map[string]*workqueue.RateLimitingInterface),
	}
}

// Start implements Runnable
func (r *MirrorReconciler) Start(ctx context.Context) error {
	log := r.Log
	log.Info("Starting mirror reconciler")

	// Get initial list of clusters
	clusters := r.Provider.ListClusters()
	log.Info("Found clusters", "count", len(clusters))

	// Set up watches for each cluster
	for clusterName, cl := range clusters {
		if err := r.setupWatch(ctx, cl, clusterName); err != nil {
			log.Error(err, "Failed to set up watch", "cluster", clusterName)
			// Continue with other clusters
		}
	}

	return nil
}

// Engage implements multicluster.Aware
func (r *MirrorReconciler) Engage(ctx context.Context, clusterName string, cl cluster.Cluster) error {
	log := r.Log.WithValues("cluster", clusterName)
	log.Info("Setting up watch for new cluster")

	return r.setupWatch(ctx, cl, clusterName)
}

// setupWatch sets up a watch for mirrored resources in a cluster
func (r *MirrorReconciler) setupWatch(ctx context.Context, cl cluster.Cluster, clusterName string) error {
	log := r.Log.WithValues("cluster", clusterName)

	// Set up watches for both resource types
	resourceTypes := []struct {
		obj  client.Object
		kind string
	}{
		{&crdv1alpha1.Failover{}, "Failover"},
		{&crdv1alpha1.FailoverGroup{}, "FailoverGroup"},
	}

	for _, rt := range resourceTypes {
		// Get the informer for the resource type
		informer, err := cl.GetCache().GetInformer(ctx, rt.obj)
		if err != nil {
			return err
		}

		// Add event handlers
		_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				resource := obj.(runtime.Object)
				log.Info("Resource added",
					"name", resource.(metav1.Object).GetName(),
					"type", rt.kind)
				r.handleResourceEvent(ctx, cl, resource, "add", rt.kind)
			},
			UpdateFunc: func(old, new interface{}) {
				newResource := new.(runtime.Object)
				log.Info("Resource updated",
					"name", newResource.(metav1.Object).GetName(),
					"type", rt.kind)
				r.handleResourceEvent(ctx, cl, newResource, "update", rt.kind)
			},
			DeleteFunc: func(obj interface{}) {
				resource, ok := obj.(runtime.Object)
				if !ok {
					if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
						resource, ok = tombstone.Obj.(runtime.Object)
						if !ok {
							log.Error(nil, "Error decoding resource from tombstone")
							return
						}
					} else {
						log.Error(nil, "Error decoding resource")
						return
					}
				}
				log.Info("Resource deleted",
					"name", resource.(metav1.Object).GetName(),
					"type", rt.kind)
				r.handleResourceEvent(ctx, cl, resource, "delete", rt.kind)
			},
		})

		if err != nil {
			return err
		}
	}

	return nil
}

// handleResourceEvent processes a resource event
func (r *MirrorReconciler) handleResourceEvent(ctx context.Context, cl cluster.Cluster, resource runtime.Object, eventType string, resourceType string) {
	log := r.Log.WithValues(
		"resource", resource.(metav1.Object).GetName(),
		"event", eventType,
		"type", resourceType,
	)

	// Get the cluster name from the provider
	clusterName := ""
	for name, cluster := range r.Provider.ListClusters() {
		if cluster == cl {
			clusterName = name
			break
		}
	}
	if clusterName == "" {
		log.Error(nil, "Failed to get cluster name")
		return
	}

	// Handle the event based on type
	switch eventType {
	case "add":
		r.handleResourceAdd(ctx, cl, resource, clusterName, resourceType)
	case "update":
		r.handleResourceUpdate(ctx, cl, resource, clusterName, resourceType)
	case "delete":
		r.handleResourceDelete(ctx, cl, resource, clusterName, resourceType)
	}
}

// handleResourceAdd handles resource creation events
func (r *MirrorReconciler) handleResourceAdd(ctx context.Context, cl cluster.Cluster, resource runtime.Object, clusterName string, resourceType string) {
	log := r.Log.WithValues("resource", resource.(metav1.Object).GetName())

	// Get the resource type
	log = log.WithValues("type", resourceType)

	// Convert to client.Object
	clientObj, ok := resource.(client.Object)
	if !ok {
		log.Error(nil, "Failed to convert resource to client.Object")
		return
	}

	// Check if this is a new resource (no source of truth cluster set)
	var sotCluster string
	switch r := clientObj.(type) {
	case *crdv1alpha1.Failover:
		sotCluster = r.Spec.SourceOfTruthCluster
	case *crdv1alpha1.FailoverGroup:
		sotCluster = r.Spec.SourceOfTruthCluster
	default:
		log.Error(nil, "Unknown resource type")
		return
	}

	if sotCluster == "" {
		// This is a new resource, set the source of truth cluster
		switch r := clientObj.(type) {
		case *crdv1alpha1.Failover:
			r.Spec.SourceOfTruthCluster = clusterName
		case *crdv1alpha1.FailoverGroup:
			r.Spec.SourceOfTruthCluster = clusterName
		}

		// Retry logic for setting the source of truth cluster
		for i := 0; i < maxRetries; i++ {
			if err := cl.GetClient().Update(ctx, clientObj); err != nil {
				if errors.IsConflict(err) {
					// Get the latest version and try again
					if err := cl.GetClient().Get(ctx, client.ObjectKey{
						Namespace: clientObj.GetNamespace(),
						Name:      clientObj.GetName(),
					}, clientObj); err != nil {
						log.Error(err, "Failed to get latest version after conflict")
						return
					}
					// Reapply our changes
					switch r := clientObj.(type) {
					case *crdv1alpha1.Failover:
						r.Spec.SourceOfTruthCluster = clusterName
					case *crdv1alpha1.FailoverGroup:
						r.Spec.SourceOfTruthCluster = clusterName
					}
					continue
				}
				log.Error(err, "Failed to set source of truth cluster")
				return
			}
			log.Info("Set source of truth cluster", "cluster", clusterName)
			break
		}
	}

	// Handle initial sync based on source of truth cluster
	if sotCluster != clusterName {
		log.Info("Processing add from non-SOT cluster",
			"sotCluster", sotCluster)

		// Get the SOT cluster client
		sotClient, err := r.Provider.Get(ctx, sotCluster)
		if err != nil {
			log.Error(err, "Failed to get SOT cluster client")
			return
		}

		// Create a deep copy of the resource
		targetResource := clientObj.DeepCopyObject().(client.Object)

		// Clean up metadata for the target resource
		if metaObj, ok := targetResource.(metav1.Object); ok {
			metaObj.SetResourceVersion("")
			metaObj.SetUID("")
			metaObj.SetSelfLink("")
			metaObj.SetCreationTimestamp(metav1.Time{})
			metaObj.SetManagedFields(nil)
		}

		// Try to get the resource in the SOT cluster
		err = sotClient.GetClient().Get(ctx, client.ObjectKeyFromObject(targetResource), targetResource)
		if err != nil {
			if errors.IsNotFound(err) {
				// Resource doesn't exist in SOT cluster, create it
				log.Info("Resource not found in SOT cluster, creating it")
				if err := sotClient.GetClient().Create(ctx, targetResource); err != nil {
					if errors.IsAlreadyExists(err) {
						// Resource was created by another process, get it and update
						log.Info("Resource already exists in SOT cluster, getting latest version")
						if err := sotClient.GetClient().Get(ctx, client.ObjectKeyFromObject(targetResource), targetResource); err != nil {
							log.Error(err, "Failed to get existing resource in SOT cluster")
							return
						}
					} else {
						log.Error(err, "Failed to create resource in SOT cluster")
						return
					}
				}
				log.Info("Created/Retrieved resource in SOT cluster")
			} else {
				log.Error(err, "Failed to get resource in SOT cluster")
				return
			}
		}

		// Update the resource in SOT cluster
		// For spec updates, we need to preserve the status
		switch r := targetResource.(type) {
		case *crdv1alpha1.Failover:
			if source, ok := clientObj.(*crdv1alpha1.Failover); ok {
				// Preserve status while updating spec
				status := r.Status
				r.Spec = source.Spec
				r.Status = status
			}
		case *crdv1alpha1.FailoverGroup:
			if source, ok := clientObj.(*crdv1alpha1.FailoverGroup); ok {
				// Preserve status while updating spec
				status := r.Status
				r.Spec = source.Spec
				r.Status = status
			}
		}

		// Retry logic for updating the resource
		for i := 0; i < maxRetries; i++ {
			if err := sotClient.GetClient().Update(ctx, targetResource); err != nil {
				if errors.IsConflict(err) {
					// Get the latest version and try again
					log.Info("Conflict updating resource in SOT cluster, getting latest version")
					if err := sotClient.GetClient().Get(ctx, client.ObjectKeyFromObject(targetResource), targetResource); err != nil {
						log.Error(err, "Failed to get latest version after conflict")
						return
					}
					// Reapply our changes while preserving status
					switch r := targetResource.(type) {
					case *crdv1alpha1.Failover:
						if source, ok := clientObj.(*crdv1alpha1.Failover); ok {
							status := r.Status
							r.Spec = source.Spec
							r.Status = status
						}
					case *crdv1alpha1.FailoverGroup:
						if source, ok := clientObj.(*crdv1alpha1.FailoverGroup); ok {
							status := r.Status
							r.Spec = source.Spec
							r.Status = status
						}
					}
					continue
				}
				log.Error(err, "Failed to update resource in SOT cluster")
				return
			}
			log.Info("Updated resource in SOT cluster")
			break
		}

		// Sync to all other clusters
		clusters := r.Provider.ListClusters()
		for targetCluster := range clusters {
			if targetCluster != clusterName && targetCluster != sotCluster {
				if err := r.syncToCluster(ctx, targetResource, targetCluster); err != nil {
					log.Error(err, "Failed to sync to cluster", "cluster", targetCluster)
					// Continue with other clusters
				}
			}
		}
		return
	}

	// If we get here, this is the SOT cluster
	log.Info("Processing add from SOT cluster")

	// Sync to all other clusters
	clusters := r.Provider.ListClusters()
	for targetCluster := range clusters {
		if targetCluster != clusterName {
			if err := r.syncToCluster(ctx, clientObj, targetCluster); err != nil {
				log.Error(err, "Failed to sync to cluster", "cluster", targetCluster)
				// Continue with other clusters
			}
		}
	}
}

// handleResourceUpdate handles resource update events
func (r *MirrorReconciler) handleResourceUpdate(ctx context.Context, cl cluster.Cluster, resource runtime.Object, clusterName string, resourceType string) {
	log := r.Log.WithValues("resource", resource.(metav1.Object).GetName())

	// Get the resource type
	log = log.WithValues("type", resourceType)

	// Convert to client.Object
	clientObj, ok := resource.(client.Object)
	if !ok {
		log.Error(nil, "Failed to convert resource to client.Object")
		return
	}

	// Get the source of truth cluster from spec
	var sotCluster string
	switch r := clientObj.(type) {
	case *crdv1alpha1.Failover:
		sotCluster = r.Spec.SourceOfTruthCluster
	case *crdv1alpha1.FailoverGroup:
		sotCluster = r.Spec.SourceOfTruthCluster
	default:
		log.Error(nil, "Unknown resource type")
		return
	}

	// Only process if this is the source of truth cluster
	if sotCluster != clusterName {
		log.Info("Processing update from non-SOT cluster",
			"sotCluster", sotCluster)

		// Get the SOT cluster client
		sotClient, err := r.Provider.Get(ctx, sotCluster)
		if err != nil {
			log.Error(err, "Failed to get SOT cluster client")
			return
		}

		// Create a deep copy of the resource
		targetResource := clientObj.DeepCopyObject().(client.Object)

		// Clean up metadata for the target resource
		if metaObj, ok := targetResource.(metav1.Object); ok {
			metaObj.SetResourceVersion("")
			metaObj.SetUID("")
			metaObj.SetSelfLink("")
			metaObj.SetCreationTimestamp(metav1.Time{})
			metaObj.SetManagedFields(nil)
		}

		// Try to get the resource in the SOT cluster
		err = sotClient.GetClient().Get(ctx, client.ObjectKeyFromObject(targetResource), targetResource)
		if err != nil {
			if errors.IsNotFound(err) {
				// Resource doesn't exist in SOT cluster, create it
				log.Info("Resource not found in SOT cluster, creating it")
				if err := sotClient.GetClient().Create(ctx, targetResource); err != nil {
					if errors.IsAlreadyExists(err) {
						// Resource was created by another process, get it and update
						log.Info("Resource already exists in SOT cluster, getting latest version")
						if err := sotClient.GetClient().Get(ctx, client.ObjectKeyFromObject(targetResource), targetResource); err != nil {
							log.Error(err, "Failed to get existing resource in SOT cluster")
							return
						}
					} else {
						log.Error(err, "Failed to create resource in SOT cluster")
						return
					}
				}
				log.Info("Created/Retrieved resource in SOT cluster")
			} else {
				log.Error(err, "Failed to get resource in SOT cluster")
				return
			}
		}

		// Update the resource in SOT cluster
		// For spec updates, we need to preserve the status
		switch r := targetResource.(type) {
		case *crdv1alpha1.Failover:
			if source, ok := clientObj.(*crdv1alpha1.Failover); ok {
				// Preserve status while updating spec
				status := r.Status
				r.Spec = source.Spec
				r.Status = status
			}
		case *crdv1alpha1.FailoverGroup:
			if source, ok := clientObj.(*crdv1alpha1.FailoverGroup); ok {
				// Preserve status while updating spec
				status := r.Status
				r.Spec = source.Spec
				r.Status = status
			}
		}

		// Retry logic for updating the resource
		for i := 0; i < maxRetries; i++ {
			if err := sotClient.GetClient().Update(ctx, targetResource); err != nil {
				if errors.IsConflict(err) {
					// Get the latest version and try again
					log.Info("Conflict updating resource in SOT cluster, getting latest version")
					if err := sotClient.GetClient().Get(ctx, client.ObjectKeyFromObject(targetResource), targetResource); err != nil {
						log.Error(err, "Failed to get latest version after conflict")
						return
					}
					// Reapply our changes while preserving status
					switch r := targetResource.(type) {
					case *crdv1alpha1.Failover:
						if source, ok := clientObj.(*crdv1alpha1.Failover); ok {
							status := r.Status
							r.Spec = source.Spec
							r.Status = status
						}
					case *crdv1alpha1.FailoverGroup:
						if source, ok := clientObj.(*crdv1alpha1.FailoverGroup); ok {
							status := r.Status
							r.Spec = source.Spec
							r.Status = status
						}
					}
					continue
				}
				log.Error(err, "Failed to update resource in SOT cluster")
				return
			}
			log.Info("Updated resource in SOT cluster")
			break
		}

		// Sync to all other clusters
		clusters := r.Provider.ListClusters()
		for targetCluster := range clusters {
			if targetCluster != clusterName && targetCluster != sotCluster {
				if err := r.syncToCluster(ctx, targetResource, targetCluster); err != nil {
					log.Error(err, "Failed to sync to cluster", "cluster", targetCluster)
					// Continue with other clusters
				}
			}
		}
		return
	}

	// If we get here, this is the SOT cluster
	log.Info("Processing update from SOT cluster")

	// Sync to all other clusters
	clusters := r.Provider.ListClusters()
	for targetCluster := range clusters {
		if targetCluster != clusterName {
			if err := r.syncToCluster(ctx, clientObj, targetCluster); err != nil {
				log.Error(err, "Failed to sync to cluster", "cluster", targetCluster)
				// Continue with other clusters
			}
		}
	}
}

// handleResourceDelete handles resource deletion events
func (r *MirrorReconciler) handleResourceDelete(ctx context.Context, cl cluster.Cluster, resource runtime.Object, clusterName string, resourceType string) {
	log := r.Log.WithValues("resource", resource.(metav1.Object).GetName())

	// Get the resource type
	log = log.WithValues("type", resourceType)

	// Convert to client.Object
	clientObj, ok := resource.(client.Object)
	if !ok {
		log.Error(nil, "Failed to convert resource to client.Object")
		return
	}

	// Get the source of truth cluster from spec
	var sotCluster string
	switch r := clientObj.(type) {
	case *crdv1alpha1.Failover:
		sotCluster = r.Spec.SourceOfTruthCluster
	case *crdv1alpha1.FailoverGroup:
		sotCluster = r.Spec.SourceOfTruthCluster
	default:
		log.Error(nil, "Unknown resource type")
		return
	}

	// Get all clusters
	clusters := r.Provider.ListClusters()

	// Handle deletion based on source of truth cluster
	if sotCluster == clusterName {
		log.Info("Processing deletion from SOT cluster")

		// Delete from all other clusters
		for targetCluster := range clusters {
			if targetCluster != clusterName {
				// Get the target cluster client
				targetClient, err := r.Provider.Get(ctx, targetCluster)
				if err != nil {
					log.Error(err, "Failed to get cluster client", "cluster", targetCluster)
					continue
				}

				// Try to delete the resource from the target cluster
				if err := targetClient.GetClient().Delete(ctx, clientObj); err != nil {
					if errors.IsNotFound(err) {
						log.Info("Resource already deleted from cluster", "cluster", targetCluster)
						continue
					}
					log.Error(err, "Failed to delete resource from cluster", "cluster", targetCluster)
				} else {
					log.Info("Deleted resource from cluster", "cluster", targetCluster)
				}
			}
		}
	} else {
		log.Info("Processing deletion from non-SOT cluster")

		// Get the SOT cluster client
		sotClient, err := r.Provider.Get(ctx, sotCluster)
		if err != nil {
			log.Error(err, "Failed to get SOT cluster client")
			return
		}

		// Try to delete the resource from the SOT cluster
		if err := sotClient.GetClient().Delete(ctx, clientObj); err != nil {
			if errors.IsNotFound(err) {
				log.Info("Resource already deleted from SOT cluster")
			} else {
				log.Error(err, "Failed to delete resource from SOT cluster")
				return
			}
		} else {
			log.Info("Deleted resource from SOT cluster")
		}

		// Delete from all other clusters
		for targetCluster := range clusters {
			if targetCluster != clusterName && targetCluster != sotCluster {
				// Get the target cluster client
				targetClient, err := r.Provider.Get(ctx, targetCluster)
				if err != nil {
					log.Error(err, "Failed to get cluster client", "cluster", targetCluster)
					continue
				}

				// Try to delete the resource from the target cluster
				if err := targetClient.GetClient().Delete(ctx, clientObj); err != nil {
					if errors.IsNotFound(err) {
						log.Info("Resource already deleted from cluster", "cluster", targetCluster)
						continue
					}
					log.Error(err, "Failed to delete resource from cluster", "cluster", targetCluster)
				} else {
					log.Info("Deleted resource from cluster", "cluster", targetCluster)
				}
			}
		}
	}
}

// syncToCluster syncs a resource to a target cluster
func (r *MirrorReconciler) syncToCluster(ctx context.Context, resource client.Object, targetCluster string) error {
	log := r.Log.WithValues(
		"resource", resource.GetName(),
		"targetCluster", targetCluster,
	)

	// Get the target cluster client
	cl, err := r.Provider.Get(ctx, targetCluster)
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %w", err)
	}

	// Create a deep copy of the resource
	targetResource := resource.DeepCopyObject().(client.Object)

	// Clean up metadata for target cluster
	if meta, ok := targetResource.(metav1.Object); ok {
		meta.SetResourceVersion("")              // Remove resource version
		meta.SetUID("")                          // Remove UID
		meta.SetSelfLink("")                     // Remove self link
		meta.SetCreationTimestamp(metav1.Time{}) // Remove creation timestamp
		meta.SetManagedFields(nil)               // Remove managed fields

		// Copy all annotations from source resource
		sourceAnnotations := resource.(metav1.Object).GetAnnotations()
		if sourceAnnotations == nil {
			sourceAnnotations = make(map[string]string)
		}
		meta.SetAnnotations(sourceAnnotations)
	}

	// Try to get the resource in the target cluster
	err = cl.GetClient().Get(ctx, client.ObjectKey{
		Namespace: targetResource.GetNamespace(),
		Name:      targetResource.GetName(),
	}, targetResource)

	if err != nil {
		if errors.IsNotFound(err) {
			// Resource doesn't exist, create it
			if err := cl.GetClient().Create(ctx, targetResource); err != nil {
				return fmt.Errorf("failed to create resource: %w", err)
			}
			log.Info("Created resource in target cluster")
			return nil
		}
		return fmt.Errorf("failed to get resource: %w", err)
	}

	// Resource exists, update it
	// For spec updates, we need to preserve the status
	switch r := targetResource.(type) {
	case *crdv1alpha1.Failover:
		if source, ok := resource.(*crdv1alpha1.Failover); ok {
			// Update spec and status from source
			r.Spec = source.Spec
			r.Status = source.Status
		}
	case *crdv1alpha1.FailoverGroup:
		if source, ok := resource.(*crdv1alpha1.FailoverGroup); ok {
			// Update spec and status from source
			r.Spec = source.Spec
			r.Status = source.Status
		}
	}

	// Copy all annotations from source resource
	if meta, ok := targetResource.(metav1.Object); ok {
		sourceAnnotations := resource.(metav1.Object).GetAnnotations()
		if sourceAnnotations == nil {
			sourceAnnotations = make(map[string]string)
		}
		meta.SetAnnotations(sourceAnnotations)
	}

	// Update spec first
	if err := cl.GetClient().Update(ctx, targetResource); err != nil {
		if errors.IsConflict(err) {
			// Get the latest version and try again
			if err := cl.GetClient().Get(ctx, client.ObjectKeyFromObject(targetResource), targetResource); err != nil {
				return fmt.Errorf("failed to get latest version after conflict: %w", err)
			}
			// Reapply our changes
			switch r := targetResource.(type) {
			case *crdv1alpha1.Failover:
				if source, ok := resource.(*crdv1alpha1.Failover); ok {
					r.Spec = source.Spec
				}
			case *crdv1alpha1.FailoverGroup:
				if source, ok := resource.(*crdv1alpha1.FailoverGroup); ok {
					r.Spec = source.Spec
				}
			}
			if err := cl.GetClient().Update(ctx, targetResource); err != nil {
				return fmt.Errorf("failed to update spec after conflict resolution: %w", err)
			}
		} else {
			return fmt.Errorf("failed to update spec: %w", err)
		}
	}

	// Update status separately
	switch r := targetResource.(type) {
	case *crdv1alpha1.Failover:
		if source, ok := resource.(*crdv1alpha1.Failover); ok {
			r.Status = source.Status
			if err := cl.GetClient().Status().Update(ctx, r); err != nil {
				if errors.IsConflict(err) {
					// Get the latest version and try again
					if err := cl.GetClient().Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
						return fmt.Errorf("failed to get latest version after status conflict: %w", err)
					}
					r.Status = source.Status
					if err := cl.GetClient().Status().Update(ctx, r); err != nil {
						return fmt.Errorf("failed to update status after conflict resolution: %w", err)
					}
				} else {
					return fmt.Errorf("failed to update status: %w", err)
				}
			}
		}
	case *crdv1alpha1.FailoverGroup:
		if source, ok := resource.(*crdv1alpha1.FailoverGroup); ok {
			r.Status = source.Status
			if err := cl.GetClient().Status().Update(ctx, r); err != nil {
				if errors.IsConflict(err) {
					// Get the latest version and try again
					if err := cl.GetClient().Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
						return fmt.Errorf("failed to get latest version after status conflict: %w", err)
					}
					r.Status = source.Status
					if err := cl.GetClient().Status().Update(ctx, r); err != nil {
						return fmt.Errorf("failed to update status after conflict resolution: %w", err)
					}
				} else {
					return fmt.Errorf("failed to update status: %w", err)
				}
			}
		}
	}

	log.Info("Updated resource in target cluster")
	return nil
}

// queueStatusSync queues a resource for status sync
func (r *MirrorReconciler) queueStatusSync(resource client.Object, clusterName string) {
	queue, exists := r.statusSyncQueues[clusterName]
	if !exists {
		r.Log.Error(nil, "Status sync queue not found for cluster", "cluster", clusterName)
		return
	}

	key := types.NamespacedName{
		Namespace: resource.GetNamespace(),
		Name:      resource.GetName(),
	}
	(*queue).Add(key)
	r.Log.Info("Added resource to status sync queue",
		"resource", key,
		"cluster", clusterName)
}

// startStatusSyncWorkers starts the status sync workers
func (r *MirrorReconciler) startStatusSyncWorkers(ctx context.Context) {
	for clusterName, queue := range r.statusSyncQueues {
		go r.processStatusSyncQueue(ctx, clusterName, queue)
	}
}

// processStatusSyncQueue processes the status sync queue
func (r *MirrorReconciler) processStatusSyncQueue(ctx context.Context, clusterName string, queue *workqueue.RateLimitingInterface) {
	log := r.Log.WithValues("cluster", clusterName)
	log.Info("Starting status sync worker")

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("Status sync worker stopped")
			return
		case <-ticker.C:
			log.V(2).Info("Processing status sync queue")
			// Process all items in the queue
			for {
				item, shutdown := (*queue).Get()
				if shutdown {
					log.Info("Status sync queue shutdown")
					return
				}

				key := item.(types.NamespacedName)
				log.Info("Processing status sync for resource",
					"resource", key,
					"cluster", clusterName)

				if err := r.syncStatus(ctx, key, clusterName); err != nil {
					log.Error(err, "Failed to sync status", "key", key)
					(*queue).AddRateLimited(key)
				} else {
					log.Info("Successfully synced status",
						"resource", key,
						"cluster", clusterName)
					(*queue).Forget(key)
				}
				(*queue).Done(item)
			}
		}
	}
}

// syncStatus syncs the status of a resource
func (r *MirrorReconciler) syncStatus(ctx context.Context, key types.NamespacedName, clusterName string) error {
	log := r.Log.WithValues(
		"key", key,
		"cluster", clusterName,
	)

	// Get the resource from the source cluster
	cl, err := r.Provider.Get(ctx, clusterName)
	if err != nil {
		return fmt.Errorf("failed to get cluster %s: %w", clusterName, err)
	}

	// Try to get as Failover first
	var failover crdv1alpha1.Failover
	if err := cl.GetClient().Get(ctx, key, &failover); err == nil {
		log.Info("Found Failover resource, syncing status")
		// Sync Failover status
		return r.syncFailoverStatus(ctx, &failover, clusterName)
	}

	// If not a Failover, try as FailoverGroup
	var failoverGroup crdv1alpha1.FailoverGroup
	if err := cl.GetClient().Get(ctx, key, &failoverGroup); err != nil {
		return fmt.Errorf("failed to get resource: %w", err)
	}

	log.Info("Found FailoverGroup resource, syncing status")
	// Sync FailoverGroup status
	return r.syncFailoverGroupStatus(ctx, &failoverGroup, clusterName)
}

// syncFailoverStatus syncs the status of a Failover resource
func (r *MirrorReconciler) syncFailoverStatus(ctx context.Context, failover *crdv1alpha1.Failover, clusterName string) error {
	log := r.Log.WithValues(
		"failover", failover.Name,
		"cluster", clusterName,
	)

	// Sync to all other clusters
	clusters := r.Provider.ListClusters()
	for targetCluster := range clusters {
		if targetCluster != clusterName {
			log.Info("Syncing status to target cluster",
				"targetCluster", targetCluster)

			cl, err := r.Provider.Get(ctx, targetCluster)
			if err != nil {
				log.Error(err, "Failed to get cluster client", "cluster", targetCluster)
				continue
			}

			// Get the resource in the target cluster
			var targetFailover crdv1alpha1.Failover
			if err := cl.GetClient().Get(ctx, client.ObjectKey{
				Namespace: failover.Namespace,
				Name:      failover.Name,
			}, &targetFailover); err != nil {
				if errors.IsNotFound(err) {
					log.V(2).Info("Resource not found in target cluster, skipping",
						"targetCluster", targetCluster)
					continue // Resource doesn't exist in target cluster, skip
				}
				log.Error(err, "Failed to get resource in target cluster", "cluster", targetCluster)
				continue
			}

			// Update the status
			targetFailover.Status = failover.Status
			if err := cl.GetClient().Status().Update(ctx, &targetFailover); err != nil {
				log.Error(err, "Failed to update status in target cluster", "cluster", targetCluster)
				continue
			}

			log.Info("Successfully synced status to target cluster",
				"targetCluster", targetCluster)
		}
	}

	return nil
}

// syncFailoverGroupStatus syncs the status of a FailoverGroup resource
func (r *MirrorReconciler) syncFailoverGroupStatus(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup, clusterName string) error {
	log := r.Log.WithValues(
		"failovergroup", failoverGroup.Name,
		"cluster", clusterName,
	)

	// Sync to all other clusters
	clusters := r.Provider.ListClusters()
	for targetCluster := range clusters {
		if targetCluster != clusterName {
			log.Info("Syncing status to target cluster",
				"targetCluster", targetCluster)

			cl, err := r.Provider.Get(ctx, targetCluster)
			if err != nil {
				log.Error(err, "Failed to get cluster client", "cluster", targetCluster)
				continue
			}

			// Get the resource in the target cluster
			var targetFailoverGroup crdv1alpha1.FailoverGroup
			if err := cl.GetClient().Get(ctx, client.ObjectKey{
				Namespace: failoverGroup.Namespace,
				Name:      failoverGroup.Name,
			}, &targetFailoverGroup); err != nil {
				if errors.IsNotFound(err) {
					log.V(2).Info("Resource not found in target cluster, skipping",
						"targetCluster", targetCluster)
					continue // Resource doesn't exist in target cluster, skip
				}
				log.Error(err, "Failed to get resource in target cluster", "cluster", targetCluster)
				continue
			}

			// Update the status
			targetFailoverGroup.Status = failoverGroup.Status
			if err := cl.GetClient().Status().Update(ctx, &targetFailoverGroup); err != nil {
				log.Error(err, "Failed to update status in target cluster", "cluster", targetCluster)
				continue
			}

			log.Info("Successfully synced status to target cluster",
				"targetCluster", targetCluster)
		}
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MirrorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&crdv1alpha1.Failover{}).
		For(&crdv1alpha1.FailoverGroup{}).
		Complete(r)
}

// Helper functions
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) []string {
	result := make([]string, 0, len(slice))
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *MirrorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("resource", req.NamespacedName)

	// Try to get the resource as a Failover first
	var failover crdv1alpha1.Failover
	if err := r.Get(ctx, req.NamespacedName, &failover); err == nil {
		// Get the cluster this resource is in
		clusterName := req.NamespacedName.Namespace
		cl, err := r.Provider.Get(ctx, clusterName)
		if err != nil {
			log.Error(err, "Failed to get cluster", "cluster", clusterName)
			return ctrl.Result{}, err
		}

		// Handle the resource
		r.handleResourceEvent(ctx, cl, &failover, "update", "Failover")
		return ctrl.Result{}, nil
	}

	// If not a Failover, try as a FailoverGroup
	var failoverGroup crdv1alpha1.FailoverGroup
	if err := r.Get(ctx, req.NamespacedName, &failoverGroup); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Get the cluster this resource is in
	clusterName := req.NamespacedName.Namespace
	cl, err := r.Provider.Get(ctx, clusterName)
	if err != nil {
		log.Error(err, "Failed to get cluster", "cluster", clusterName)
		return ctrl.Result{}, err
	}

	// Handle the resource
	r.handleResourceEvent(ctx, cl, &failoverGroup, "update", "FailoverGroup")

	return ctrl.Result{}, nil
}
