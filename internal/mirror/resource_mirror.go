package mirror

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"

	crdv1alpha1 "github.com/christensenjairus/Multicluster-Failover-Operator/api/v1alpha1"
	kubeconfigprovider "sigs.k8s.io/multicluster-runtime/providers/kubeconfig"
)

// ResourceMirror handles cross-cluster resource mirroring
type ResourceMirror struct {
	Log      logr.Logger
	Provider *kubeconfigprovider.Provider
}

// NewResourceMirror creates a new ResourceMirror
func NewResourceMirror(provider *kubeconfigprovider.Provider) *ResourceMirror {
	return &ResourceMirror{
		Log:      log.FromContext(context.Background()).WithName("resource-mirror"),
		Provider: provider,
	}
}

// MirrorObject mirrors an object from the source cluster to all other clusters.
// If the object doesn't exist in the source cluster, it will allow bi-directional sync
// to discover the object from other clusters.
func (m *ResourceMirror) MirrorObject(ctx context.Context, sourceClusterName string, obj client.Object) error {
	log := m.Log.WithValues(
		"kind", obj.GetObjectKind().GroupVersionKind().Kind,
		"name", obj.GetName(),
		"namespace", obj.GetNamespace(),
		"sourceCluster", sourceClusterName,
	)

	// Get all clusters
	clusters := m.Provider.ListClusters()

	// Determine the actual source of truth based on the resource type
	actualSourceCluster := sourceClusterName
	switch obj.(type) {
	case *crdv1alpha1.FailoverGroup:
		// For FailoverGroup, use the active cluster as source of truth
		if failoverGroup, ok := obj.(*crdv1alpha1.FailoverGroup); ok {
			if failoverGroup.Status.GlobalState.ActiveCluster != "" {
				actualSourceCluster = failoverGroup.Status.GlobalState.ActiveCluster
				log.V(2).Info("Using active cluster as source of truth", "activeCluster", actualSourceCluster)
			}
		}
	case *crdv1alpha1.Failover:
		// For Failover, use the target cluster as source of truth
		if failover, ok := obj.(*crdv1alpha1.Failover); ok {
			actualSourceCluster = failover.Spec.TargetCluster
			log.V(2).Info("Using target cluster as source of truth", "targetCluster", actualSourceCluster)
		}
	}

	// First check if the object exists in the source cluster
	sourceCluster, exists := clusters[actualSourceCluster]
	if !exists {
		return fmt.Errorf("source cluster %s not found", actualSourceCluster)
	}

	// Check if object exists in source cluster
	sourceObj := obj.DeepCopyObject().(client.Object)
	err := sourceCluster.GetClient().Get(ctx, client.ObjectKey{
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}, sourceObj)

	// If object doesn't exist in source cluster, try to find it in other clusters
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to check if object exists in source cluster: %w", err)
		}

		// Object doesn't exist in source cluster, try to find it in other clusters
		log.Info("Object not found in source cluster, attempting to discover from other clusters")
		for clusterName, targetCluster := range clusters {
			if clusterName == actualSourceCluster {
				continue
			}

			discoveredObj := obj.DeepCopyObject().(client.Object)
			err := targetCluster.GetClient().Get(ctx, client.ObjectKey{
				Namespace: obj.GetNamespace(),
				Name:      obj.GetName(),
			}, discoveredObj)

			if err == nil {
				// Found the object in another cluster, create it in source cluster
				log.Info("Found object in cluster, creating in source cluster", "discoveryCluster", clusterName)
				discoveredObj.SetUID("")
				discoveredObj.SetResourceVersion("")
				if err := sourceCluster.GetClient().Create(ctx, discoveredObj); err != nil {
					if !errors.IsAlreadyExists(err) {
						log.Error(err, "Failed to create discovered object in source cluster")
						continue
					}
				}
				// Now that we've created it in the source cluster, proceed with normal one-way sync
				sourceObj = discoveredObj
				break
			}
		}
	}

	// Create a deep copy of the object for mirroring
	mirroredObj := sourceObj.DeepCopyObject().(client.Object)

	// Mirror to each cluster
	for clusterName, targetCluster := range clusters {
		// Skip the source cluster
		if clusterName == actualSourceCluster {
			continue
		}

		log.V(2).Info("Mirroring object to cluster", "targetCluster", clusterName)

		// Check if object exists in target cluster
		existingObj := obj.DeepCopyObject().(client.Object)
		err := targetCluster.GetClient().Get(ctx, client.ObjectKey{
			Namespace: obj.GetNamespace(),
			Name:      obj.GetName(),
		}, existingObj)

		if err != nil {
			if client.IgnoreNotFound(err) != nil {
				log.V(2).Error(err, "Failed to check if object exists in target cluster", "targetCluster", clusterName)
				continue
			}
			// Object doesn't exist, create it
			// Clear UID and resourceVersion for new objects
			mirroredObj.SetUID("")
			mirroredObj.SetResourceVersion("")
			if err := targetCluster.GetClient().Create(ctx, mirroredObj); err != nil {
				if !errors.IsAlreadyExists(err) {
					log.V(2).Error(err, "Failed to create mirrored object in target cluster", "targetCluster", clusterName)
				}
				continue
			}
		} else {
			// Object exists, update it while preserving resourceVersion and UID
			mirroredObj.SetResourceVersion(existingObj.GetResourceVersion())
			mirroredObj.SetUID(existingObj.GetUID())

			// Update the spec
			if err := targetCluster.GetClient().Update(ctx, mirroredObj); err != nil {
				if !errors.IsConflict(err) {
					log.V(2).Error(err, "Failed to update mirrored object spec in target cluster", "targetCluster", clusterName)
				} else {
					log.V(2).Info("Object spec was already updated by another process", "targetCluster", clusterName)
				}
				continue
			}

			// Update the status separately, preserving the source cluster's status
			if statusClient, ok := targetCluster.GetClient().(client.StatusClient); ok {
				log.V(2).Info("Mirroring status update", "targetCluster", clusterName)
				// Create a deep copy of the status to preserve source cluster's state
				statusObj := mirroredObj.DeepCopyObject().(client.Object)
				// Ensure we preserve the source cluster's state by copying the entire status
				if failover, ok := statusObj.(*crdv1alpha1.Failover); ok {
					if sourceFailover, ok := sourceObj.(*crdv1alpha1.Failover); ok {
						failover.Status = sourceFailover.Status
					}
				} else if failoverGroup, ok := statusObj.(*crdv1alpha1.FailoverGroup); ok {
					if sourceFailoverGroup, ok := sourceObj.(*crdv1alpha1.FailoverGroup); ok {
						failoverGroup.Status = sourceFailoverGroup.Status
					}
				}
				if err := statusClient.Status().Update(ctx, statusObj); err != nil {
					if !errors.IsConflict(err) {
						log.V(2).Error(err, "Failed to update mirrored object status in target cluster", "targetCluster", clusterName)
					} else {
						log.V(2).Info("Object status was already updated by another process", "targetCluster", clusterName)
					}
				}
			} else {
				log.V(2).Info("Target cluster client does not support status updates", "targetCluster", clusterName)
			}
		}
	}

	return nil
}

// DeleteMirroredObject deletes a mirrored object from all other clusters
func (m *ResourceMirror) DeleteMirroredObject(ctx context.Context, sourceClusterName string, obj client.Object) error {
	log := m.Log.WithValues(
		"kind", obj.GetObjectKind().GroupVersionKind().Kind,
		"name", obj.GetName(),
		"namespace", obj.GetNamespace(),
		"sourceCluster", sourceClusterName,
	)

	// Get all clusters
	clusters := m.Provider.ListClusters()

	// Delete from each cluster
	for clusterName, targetCluster := range clusters {
		// Skip the source cluster
		if clusterName == sourceClusterName {
			continue
		}

		log.Info("Deleting mirrored object from cluster", "targetCluster", clusterName)

		// Check if object exists in target cluster
		existingObj := obj.DeepCopyObject().(client.Object)
		err := targetCluster.GetClient().Get(ctx, client.ObjectKey{
			Namespace: obj.GetNamespace(),
			Name:      obj.GetName(),
		}, existingObj)

		if err != nil {
			if client.IgnoreNotFound(err) != nil {
				log.Error(err, "Failed to check if object exists in target cluster", "targetCluster", clusterName)
				continue
			}
			// Object doesn't exist, skip
			continue
		}

		// Object exists, delete it
		if err := targetCluster.GetClient().Delete(ctx, existingObj); err != nil {
			log.Error(err, "Failed to delete mirrored object from target cluster", "targetCluster", clusterName)
			continue
		}
	}

	return nil
}

// SetupMirrorWatch sets up a watch for an object type and mirrors changes across clusters
func (m *ResourceMirror) SetupMirrorWatch(ctx context.Context, sourceClusterName string, cl cluster.Cluster, objType client.Object) error {
	log := m.Log.WithValues(
		"kind", objType.GetObjectKind().GroupVersionKind().Kind,
		"sourceCluster", sourceClusterName,
	)

	// Get the informer for the object type
	informer, err := cl.GetCache().GetInformer(ctx, objType)
	if err != nil {
		return fmt.Errorf("failed to get informer: %w", err)
	}

	// Add event handlers
	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if obj, ok := obj.(client.Object); ok {
				if err := m.MirrorObject(ctx, sourceClusterName, obj); err != nil {
					log.Error(err, "Failed to mirror added object")
				}
			}
		},
		UpdateFunc: func(old, new interface{}) {
			if obj, ok := new.(client.Object); ok {
				if err := m.MirrorObject(ctx, sourceClusterName, obj); err != nil {
					log.Error(err, "Failed to mirror updated object")
				}
			}
		},
		DeleteFunc: func(obj interface{}) {
			var clientObj client.Object
			switch t := obj.(type) {
			case client.Object:
				clientObj = t
			case cache.DeletedFinalStateUnknown:
				// Try to get the object from the tombstone
				if obj, ok := t.Obj.(client.Object); ok {
					clientObj = obj
				}
			}
			if clientObj != nil {
				if err := m.DeleteMirroredObject(ctx, sourceClusterName, clientObj); err != nil {
					log.Error(err, "Failed to delete mirrored object")
				}
			}
		},
	})

	return err
}
