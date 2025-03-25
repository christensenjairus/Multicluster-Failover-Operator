/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	stderrors "errors"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	crdv1alpha1 "github.com/christensenjairus/Multicluster-Failover-Operator/api/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	kubeconfig "sigs.k8s.io/multicluster-runtime/providers/kubeconfig"
)

// FailoverReconciler reconciles a Failover object
type FailoverReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Reference to the multicluster reconciler
	MCReconciler *kubeconfig.MulticlusterReconciler
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// +kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers/finalizers,verbs=update
func (r *FailoverReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Skip reconciliation if we're in startup mode and remote clusters might not be ready
	if !r.waitForClustersReady(ctx) {
		logger.Info("Skipping reconciliation during startup - waiting for clusters to be ready")
		return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
	}

	// Check if we're dealing with a specific remote cluster
	var clusterName string
	var targetClient client.Client
	var crdOnly bool
	var syncMode bool

	// Check if this is a CRD-only reconciliation
	if ctx.Value(CrdOnlyKey) != nil && ctx.Value(CrdOnlyKey).(bool) {
		crdOnly = true
		logger = logger.WithValues("reconcileType", "crdOnly")
	}

	// Check if this is a sync operation
	if ctx.Value(SyncModeKey) != nil && ctx.Value(SyncModeKey).(bool) {
		syncMode = true
		logger = logger.WithValues("reconcileType", "sync")
	}

	// Get cluster name from context if available
	if ctx.Value(ClusterNameKey) != nil {
		clusterName = ctx.Value(ClusterNameKey).(string)
		logger = logger.WithValues("cluster", clusterName)

		// Get the client for the specific cluster
		if r.MCReconciler != nil {
			cl, err := r.MCReconciler.GetCluster(ctx, clusterName)
			if err != nil {
				logger.Error(err, "Failed to get remote cluster", "cluster", clusterName)
				return ctrl.Result{}, err
			}
			targetClient = cl.GetClient()
			logger.Info("Using remote cluster client", "cluster", clusterName)
		} else {
			logger.Error(nil, "Remote cluster requested but no multicluster reconciler available", "cluster", clusterName)
			return ctrl.Result{}, nil
		}
	} else {
		// Use the regular client for local cluster
		targetClient = r.Client
		logger.Info("Using local cluster client")
	}

	// Get the Failover resource
	failover := &crdv1alpha1.Failover{}

	// Get the Failover from the local/target cluster
	if err := targetClient.Get(ctx, req.NamespacedName, failover); err != nil {
		if client.IgnoreNotFound(err) != nil {
			logger.Error(err, "Unable to fetch Failover")
			return ctrl.Result{}, err
		}
		logger.Info("Failover resource not found. Ignoring since object must have been deleted")
		return ctrl.Result{}, nil
	}

	logger.Info("Successfully retrieved Failover",
		"name", failover.Name,
		"namespace", failover.Namespace)

	// Add conditional logging for the target cluster
	if failover.Spec.TargetCluster != "" {
		logger.Info("Failover target cluster specified", "targetCluster", failover.Spec.TargetCluster)
	} else {
		logger.Info("No target cluster specified in Failover resource")
	}

	// If we are not the target cluster and not in sync mode, check if we need to fetch the latest version from the target cluster
	if clusterName != failover.Spec.TargetCluster && !syncMode && failover.Spec.TargetCluster != "" {
		// Get the client for the target cluster
		cl, err := r.GetCluster(ctx, logger, failover.Spec.TargetCluster)
		if err != nil {
			// Don't treat as an error - just log and move on with what we have
			logger.Info("Skipping target cluster sync due to connectivity issue",
				"targetCluster", failover.Spec.TargetCluster,
				"error", err.Error())
			// Return without error to allow requeue later
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		targetClusterClient := cl.GetClient()

		// Get the Failover from the target cluster
		targetFO := &crdv1alpha1.Failover{}
		err = targetClusterClient.Get(ctx, req.NamespacedName, targetFO)
		if err == nil {
			// We found the resource in the target cluster, check if we need to update our local copy
			if shouldUpdateFromTarget(failover, targetFO) {
				logger.Info("Updating local Failover from target cluster",
					"targetCluster", failover.Spec.TargetCluster,
					"localResourceVersion", failover.ResourceVersion,
					"targetResourceVersion", targetFO.ResourceVersion)

				// Update our local copy with changes from the target cluster
				syncCtx := context.WithValue(ctx, SyncModeKey, true)
				syncCtx = context.WithValue(syncCtx, FailoverKey, targetFO)

				// Call Reconcile with sync context
				return r.Reconcile(syncCtx, req)
			}
		} else if !apierrors.IsNotFound(err) {
			logger.Error(err, "Error getting Failover from target cluster")
		}
	}

	// If we're the target cluster, and there's a change, sync to other clusters
	if (clusterName == failover.Spec.TargetCluster || (clusterName == "" && failover.Spec.TargetCluster == "")) && !syncMode {
		// This cluster is the active one (or we're in the management cluster and no active cluster is specified)
		// Now, let's synchronize this to all other clusters
		go r.syncToOtherClusters(ctx, failover)
	}

	// CRD-only reconciliation ends here if requested
	if crdOnly {
		logger.Info("Performing CRD-only reconciliation, skipping managed resources")
		return ctrl.Result{}, nil
	}

	// Continue with reconciliation
	if clusterName == "" {
		// This is the management cluster
		err := r.reconcileFailover(ctx, logger, failover)
		return ctrl.Result{}, err
	} else {
		// This is a remote cluster
		err := r.reconcileFailoverInCluster(ctx, logger, failover, clusterName, targetClient)
		return ctrl.Result{}, err
	}
}

// shouldUpdateFromTarget determines if the local Failover should be updated from the target cluster's version
func shouldUpdateFromTarget(local, target *crdv1alpha1.Failover) bool {
	// Compare resource versions if they're in the format we expect (numerical)
	// Otherwise, we'll go with creation/modification timestamps
	// In a real implementation, you might want a more sophisticated comparison

	// Here we're assuming the target cluster version should always be used
	// You could implement more complex logic here
	return true
}

// syncToOtherClusters synchronizes a Failover from the target cluster to all other clusters
func (r *FailoverReconciler) syncToOtherClusters(ctx context.Context, failover *crdv1alpha1.Failover) {
	logger := log.FromContext(ctx).WithValues(
		"name", failover.Name,
		"namespace", failover.Namespace,
		"operation", "syncToOtherClusters")

	// List all available clusters
	clusters := r.MCReconciler.ListClustersWithLog()

	logger.Info("Syncing Failover resource to other clusters", "clusterCount", len(clusters))

	// For each cluster
	for clusterName, cl := range clusters {
		// Skip the target cluster (we're already there)
		if clusterName == failover.Spec.TargetCluster {
			logger.Info("Skipping target cluster for sync", "cluster", clusterName)
			continue
		}

		// Get the client for this cluster
		remoteClient := cl.GetClient()

		// Create a context with the cluster name for the reconciliation
		syncCtx := context.WithValue(ctx, ClusterNameKey, clusterName)
		syncCtx = context.WithValue(syncCtx, CrdOnlyKey, true) // Only sync the CRD, not actually reconcile resources

		// Check if the Failover already exists in this cluster
		existingFailover := &crdv1alpha1.Failover{}
		err := remoteClient.Get(syncCtx, types.NamespacedName{
			Namespace: failover.Namespace,
			Name:      failover.Name,
		}, existingFailover)

		if err != nil {
			if apierrors.IsNotFound(err) {
				// Doesn't exist, create it
				logger.Info("Creating Failover in remote cluster", "cluster", clusterName)
				newFailover := failover.DeepCopy()
				// Clear status and other fields that shouldn't be copied to a new resource
				newFailover.ResourceVersion = ""
				newFailover.UID = ""
				newFailover.Status = crdv1alpha1.FailoverStatus{}
				newFailover.Generation = 0

				if err := remoteClient.Create(syncCtx, newFailover); err != nil {
					logger.Error(err, "Failed to create Failover in remote cluster", "cluster", clusterName)
					continue
				}
				logger.Info("Successfully created Failover in remote cluster", "cluster", clusterName)
			} else {
				logger.Error(err, "Failed to check Failover existence in remote cluster", "cluster", clusterName)
				continue
			}
		} else {
			// Exists, update it if needed
			logger.Info("Updating Failover in remote cluster", "cluster", clusterName)
			existingFailover.Spec = failover.Spec
			if err := remoteClient.Update(syncCtx, existingFailover); err != nil {
				logger.Error(err, "Failed to update Failover in remote cluster", "cluster", clusterName)
				continue
			}
			logger.Info("Successfully updated Failover in remote cluster", "cluster", clusterName)
		}
	}
	logger.Info("Completed syncing Failover to all clusters")
}

// SetupWithManager sets up the controller with the Manager.
func (r *FailoverReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&crdv1alpha1.Failover{}).
		Complete(r); err != nil {
		return err
	}

	// We also register a background watcher for remote clusters
	if r.MCReconciler != nil {
		go r.watchRemoteClusters(context.Background())
	}

	return nil
}

// watchRemoteClusters sets up watches on all remote clusters for Failover resources
func (r *FailoverReconciler) watchRemoteClusters(ctx context.Context) {
	// Wait a bit for the manager to start and the clusters to connect
	time.Sleep(5 * time.Second)

	logger := log.FromContext(ctx)
	logger.Info("Starting remote cluster watcher for Failover resources")

	// Track which clusters we've set up watches for
	watchedClusters := make(map[string]bool)
	// Store cancel functions for each watch operation
	watchCancels := make(map[string]context.CancelFunc)

	// Set up a ticker to check for new clusters
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// Cleanup function for when we're done
	defer func() {
		// Cancel all watches
		for name, cancel := range watchCancels {
			logger.Info("Cancelling watch for cluster", "cluster", name)
			cancel()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Remote cluster watcher terminated")
			return
		case <-ticker.C:
			// Check for new clusters
			clusters := r.MCReconciler.ListClusters()
			if len(clusters) == 0 {
				continue
			}

			for name, cl := range clusters {
				// Skip clusters we're already watching
				if watchedClusters[name] {
					continue
				}

				// Create a new context for this cluster with cancel
				clusterCtx, cancel := context.WithCancel(ctx)
				clusterCtx = context.WithValue(clusterCtx, ClusterNameKey, name)
				watchCancels[name] = cancel

				// Test if the cluster cache is ready
				clusterLogger := logger.WithValues("cluster", name)

				// Create a timeout context just for testing cache readiness
				testCtx, testCancel := context.WithTimeout(clusterCtx, 15*time.Second)

				// Try to list a small resource to test if cache is synced
				var nodes corev1.NodeList
				cacheReady := true
				if err := cl.GetClient().List(testCtx, &nodes, client.Limit(1)); err != nil {
					if stderrors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Timeout") {
						clusterLogger.Info("Skipping cluster - cache not ready yet", "error", err.Error(), "timeout", "15s")
						cacheReady = false
					}
				}
				testCancel()

				if !cacheReady {
					// Try again later
					cancel()
					delete(watchCancels, name)
					continue
				}

				// Queue a reconcile for any Failover resources in the new cluster
				var failoverList crdv1alpha1.FailoverList
				if err := cl.GetClient().List(clusterCtx, &failoverList); err != nil {
					if stderrors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Timeout") {
						clusterLogger.Info("Cache sync timeout when listing resources, will retry later")
						cancel()
						delete(watchCancels, name)
						continue
					}

					clusterLogger.Error(err, "Failed to list Failovers in remote cluster")
					continue
				}

				for _, failover := range failoverList.Items {
					logger.Info("Found Failover in remote cluster, queueing reconcile",
						"cluster", name,
						"name", failover.Name,
						"namespace", failover.Namespace)

					// Actually queue a reconcile for this resource
					r.Queue(reconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      failover.Name,
							Namespace: failover.Namespace,
						},
					}, clusterCtx)
				}

				// Set up a watch for Failover resources in this cluster
				go r.watchClusterResources(clusterCtx, name, cl)

				// Mark this cluster as watched
				watchedClusters[name] = true
				logger.Info("Now watching cluster for Failover resources", "cluster", name)
			}

			// Clean up old clusters that we no longer need to watch
			for name := range watchedClusters {
				if _, exists := clusters[name]; !exists {
					delete(watchedClusters, name)
					if cancel, ok := watchCancels[name]; ok {
						cancel()
						delete(watchCancels, name)
					}
					logger.Info("Stopped watching cluster for Failover resources", "cluster", name)
				}
			}
		}
	}
}

// watchClusterResources sets up a continuous watch for resources in a specific cluster
func (r *FailoverReconciler) watchClusterResources(ctx context.Context, clusterName string, cl cluster.Cluster) {
	logger := log.FromContext(ctx).WithValues("cluster", clusterName, "operation", "watchClusterResources")
	logger.Info("Starting resource polling for cluster")

	// Map to track resource versions we've seen
	resourceVersions := make(map[string]string)

	// Poll every 5 seconds
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Polling terminated due to context cancellation")
			return
		case <-ticker.C:
			// List all Failover resources in the cluster
			var failoverList crdv1alpha1.FailoverList
			if err := cl.GetClient().List(ctx, &failoverList); err != nil {
				// Check if context was cancelled - this is a normal shutdown case
				if stderrors.Is(err, context.Canceled) {
					logger.Info("Context cancelled during list operation")
					return
				}

				logger.Error(err, "Failed to list Failovers in remote cluster")
				continue
			}

			// Check each resource
			for _, failover := range failoverList.Items {
				key := fmt.Sprintf("%s/%s", failover.Namespace, failover.Name)
				// If we haven't seen this resource before or it's changed
				if lastVersion, ok := resourceVersions[key]; !ok || lastVersion != failover.ResourceVersion {
					logger.Info("Detected new or changed Failover",
						"name", failover.Name,
						"namespace", failover.Namespace,
						"newResourceVersion", failover.ResourceVersion,
						"oldResourceVersion", lastVersion)

					// Update tracked version
					resourceVersions[key] = failover.ResourceVersion

					// Queue a reconcile
					r.Queue(reconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      failover.Name,
							Namespace: failover.Namespace,
						},
					}, ctx)
				}
			}
		}
	}
}

// reconcileFailover handles the actual domain logic for a Failover resource
func (r *FailoverReconciler) reconcileFailover(ctx context.Context, logger logr.Logger, failover *crdv1alpha1.Failover) error {
	// This is where you would implement the actual Failover reconciliation logic for the management cluster
	logger.Info("Reconciling Failover in management cluster")
	return nil
}

// reconcileFailoverInCluster handles Failover reconciliation in a remote cluster
func (r *FailoverReconciler) reconcileFailoverInCluster(ctx context.Context, logger logr.Logger, failover *crdv1alpha1.Failover, clusterName string, targetClient client.Client) error {
	// This is where you would implement the Failover reconciliation logic for a remote cluster
	logger.Info("Reconciling Failover in remote cluster")
	return nil
}

// Queue enqueues a request with the proper context
func (r *FailoverReconciler) Queue(request reconcile.Request, ctx context.Context) {
	// Create a goroutine to handle the reconciliation
	go func() {
		logger := log.FromContext(ctx).WithValues(
			"name", request.Name,
			"namespace", request.Namespace)

		logger.Info("Manually queuing reconcile for remote resource")

		// Check if context is already cancelled before proceeding
		select {
		case <-ctx.Done():
			logger.Info("Context cancelled, skipping reconciliation")
			return
		default:
			// Proceed with reconciliation
		}

		// Call Reconcile directly with the preserved context
		result, err := r.Reconcile(ctx, request)
		if err != nil {
			if stderrors.Is(err, context.Canceled) {
				logger.Info("Reconciliation cancelled due to shutdown")
			} else {
				logger.Error(err, "Error reconciling remote resource")
			}
		} else if result.Requeue {
			logger.Info("Resource needs to be requeued", "after", result.RequeueAfter)
		}
	}()
}

// GetCluster is a helper function that gracefully handles cluster connectivity issues
func (r *FailoverReconciler) GetCluster(ctx context.Context, logger logr.Logger, clusterName string) (cluster.Cluster, error) {
	if r.MCReconciler == nil {
		return nil, fmt.Errorf("multicluster reconciler not available")
	}

	cl, err := r.MCReconciler.GetCluster(ctx, clusterName)
	if err != nil {
		logger.Error(err, "Failed to get remote cluster", "cluster", clusterName)
		return nil, err
	}

	// Create a longer timeout context for testing cache readiness
	// Increase timeout from 3 to 15 seconds to allow for slower Node informers
	testCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// Test if the cache is ready by trying to list a small resource
	var nodes corev1.NodeList
	if err := cl.GetClient().List(testCtx, &nodes, client.Limit(1)); err != nil {
		if stderrors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Timeout") {
			logger.Info("Remote cluster cache not ready yet", "cluster", clusterName, "timeout", "15s")
			return nil, fmt.Errorf("remote cluster cache not ready: %w", err)
		}
		// If it's a different error, it might still be fine - we at least got a response
		logger.V(1).Info("Got response from remote cluster (error is expected)", "cluster", clusterName, "error", err)
	}

	logger.Info("Remote cluster ready", "cluster", clusterName)
	return cl, nil
}

// waitForClustersReady checks if the multicluster reconciler is ready with all needed clusters
func (r *FailoverReconciler) waitForClustersReady(ctx context.Context) bool {
	// We need a multicluster reconciler
	if r.MCReconciler == nil {
		return true // No multicluster reconciler means we only do local reconciliation
	}

	// Check if clusters are available
	clusters := r.MCReconciler.ListClusters()
	if len(clusters) > 0 {
		log.FromContext(ctx).V(1).Info("Multicluster reconciler has clusters",
			"count", len(clusters))
		return true
	}

	// No clusters yet, return false to trigger requeue
	return false
}
