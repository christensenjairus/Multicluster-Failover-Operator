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
	// We're no longer using the multicluster-runtime directly in the controllers
	// The main.go will handle setting up the controllers with mcbuilder
)

// FailoverGroupReconciler reconciles a FailoverGroup object
type FailoverGroupReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Reference to the multicluster reconciler
	MCReconciler *kubeconfig.MulticlusterReconciler
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// +kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups/finalizers,verbs=update
func (r *FailoverGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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

	// Get the FailoverGroup resource
	failoverGroup := &crdv1alpha1.FailoverGroup{}

	// Get the FailoverGroup from the target cluster
	if err := targetClient.Get(ctx, req.NamespacedName, failoverGroup); err != nil {
		if client.IgnoreNotFound(err) != nil {
			logger.Error(err, "Unable to fetch FailoverGroup")
			return ctrl.Result{}, err
		}
		logger.Info("FailoverGroup resource not found. Ignoring since object must have been deleted")
		return ctrl.Result{}, nil
	}

	logger.Info("Successfully retrieved FailoverGroup",
		"name", failoverGroup.Name,
		"namespace", failoverGroup.Namespace)

	// Add conditional logging for the active cluster
	if failoverGroup.Status.GlobalState.ActiveCluster != "" {
		logger.Info("FailoverGroup active cluster specified", "activeCluster", failoverGroup.Status.GlobalState.ActiveCluster)
	} else {
		logger.Info("No active cluster specified in FailoverGroup resource")
	}

	// If we are not the active cluster and not in sync mode, check if we need to fetch the latest version from the active cluster
	if clusterName != failoverGroup.Status.GlobalState.ActiveCluster && !syncMode && failoverGroup.Status.GlobalState.ActiveCluster != "" {
		// Get the client for the active cluster
		cl, err := r.GetCluster(ctx, logger, failoverGroup.Status.GlobalState.ActiveCluster)
		if err != nil {
			// Don't treat as an error - just log and move on with what we have
			logger.Info("Skipping active cluster sync due to connectivity issue",
				"activeCluster", failoverGroup.Status.GlobalState.ActiveCluster,
				"error", err.Error())
			// Return without error to allow requeue later
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		activeClusterClient := cl.GetClient()

		// Get the FailoverGroup from the active cluster
		activeFG := &crdv1alpha1.FailoverGroup{}
		err = activeClusterClient.Get(ctx, req.NamespacedName, activeFG)
		if err == nil {
			// We found the resource in the active cluster, check if we need to update our local copy
			if shouldUpdateFromActiveFG(failoverGroup, activeFG) {
				logger.Info("Updating local FailoverGroup from active cluster",
					"activeCluster", failoverGroup.Status.GlobalState.ActiveCluster,
					"localResourceVersion", failoverGroup.ResourceVersion,
					"activeResourceVersion", activeFG.ResourceVersion)

				// Update our local copy with changes from the active cluster
				syncCtx := context.WithValue(ctx, SyncModeKey, true)
				syncCtx = context.WithValue(syncCtx, FailoverGroupKey, activeFG)

				// Call Reconcile with sync context
				return r.Reconcile(syncCtx, req)
			}
		} else if !apierrors.IsNotFound(err) {
			logger.Error(err, "Error getting FailoverGroup from active cluster")
		}
	}

	// If we're the active cluster, and there's a change, sync to other clusters
	if (clusterName == failoverGroup.Status.GlobalState.ActiveCluster || (clusterName == "" && failoverGroup.Status.GlobalState.ActiveCluster == "")) && !syncMode {
		// This cluster is the active one (or we're in the management cluster and no active cluster is specified)
		// Now, let's synchronize this to all other clusters
		go r.syncToOtherClusters(ctx, failoverGroup)
	}

	// CRD-only reconciliation ends here if requested
	if crdOnly {
		logger.Info("Performing CRD-only reconciliation, skipping managed resources")
		return ctrl.Result{}, nil
	}

	// Continue with reconciliation
	if clusterName == "" {
		// This is the management cluster
		err := r.reconcileFailoverGroup(ctx, logger, failoverGroup)
		return ctrl.Result{}, err
	} else {
		// This is a remote cluster
		err := r.reconcileFailoverGroupInCluster(ctx, logger, failoverGroup, clusterName, targetClient)
		return ctrl.Result{}, err
	}
}

// shouldUpdateFromActiveFG determines if the local FailoverGroup should be updated from the active cluster's version
func shouldUpdateFromActiveFG(local, active *crdv1alpha1.FailoverGroup) bool {
	// Compare resource versions if they're in the format we expect (numerical)
	// Otherwise, we'll go with creation/modification timestamps
	// In a real implementation, you might want a more sophisticated comparison

	// Here we're assuming the active cluster version should always be used
	// You could implement more complex logic here
	return true
}

// syncToOtherClusters synchronizes a FailoverGroup from the active cluster to all other clusters
func (r *FailoverGroupReconciler) syncToOtherClusters(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup) {
	logger := log.FromContext(ctx).WithValues(
		"name", failoverGroup.Name,
		"namespace", failoverGroup.Namespace,
		"operation", "syncToOtherClusters")

	// List all available clusters
	clusters := r.MCReconciler.ListClustersWithLog()

	logger.Info("Syncing FailoverGroup resource to other clusters", "clusterCount", len(clusters))

	// For each cluster
	for clusterName, cl := range clusters {
		// Skip the active cluster (we're already there)
		if clusterName == failoverGroup.Status.GlobalState.ActiveCluster {
			logger.Info("Skipping active cluster for sync", "cluster", clusterName)
			continue
		}

		// Get the client for this cluster
		remoteClient := cl.GetClient()

		// Create a context with the cluster name for the reconciliation
		syncCtx := context.WithValue(ctx, ClusterNameKey, clusterName)
		syncCtx = context.WithValue(syncCtx, CrdOnlyKey, true) // Only sync the CRD, not actually reconcile resources

		// Check if the FailoverGroup already exists in this cluster
		existingFailoverGroup := &crdv1alpha1.FailoverGroup{}
		err := remoteClient.Get(syncCtx, types.NamespacedName{
			Namespace: failoverGroup.Namespace,
			Name:      failoverGroup.Name,
		}, existingFailoverGroup)

		if err != nil {
			if apierrors.IsNotFound(err) {
				// Doesn't exist, create it
				logger.Info("Creating FailoverGroup in remote cluster", "cluster", clusterName)
				newFailoverGroup := failoverGroup.DeepCopy()
				// Clear status and other fields that shouldn't be copied to a new resource
				newFailoverGroup.ResourceVersion = ""
				newFailoverGroup.UID = ""
				newFailoverGroup.Status = crdv1alpha1.FailoverGroupStatus{}
				newFailoverGroup.Generation = 0

				if err := remoteClient.Create(syncCtx, newFailoverGroup); err != nil {
					logger.Error(err, "Failed to create FailoverGroup in remote cluster", "cluster", clusterName)
					continue
				}
				logger.Info("Successfully created FailoverGroup in remote cluster", "cluster", clusterName)
			} else {
				logger.Error(err, "Failed to check FailoverGroup existence in remote cluster", "cluster", clusterName)
				continue
			}
		} else {
			// Exists, update it if needed
			logger.Info("Updating FailoverGroup in remote cluster", "cluster", clusterName)
			existingFailoverGroup.Spec = failoverGroup.Spec
			if err := remoteClient.Update(syncCtx, existingFailoverGroup); err != nil {
				logger.Error(err, "Failed to update FailoverGroup in remote cluster", "cluster", clusterName)
				continue
			}
			logger.Info("Successfully updated FailoverGroup in remote cluster", "cluster", clusterName)
		}
	}
	logger.Info("Completed syncing FailoverGroup to all clusters")
}

// SetupWithManager sets up the controller with the Manager.
func (r *FailoverGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&crdv1alpha1.FailoverGroup{}).
		Complete(r); err != nil {
		return err
	}

	// If we have a multicluster reconciler, set up a background watcher
	if r.MCReconciler != nil {
		go r.watchRemoteClusters(context.Background())
	}

	return nil
}

// watchRemoteClusters sets up watches on remote clusters for FailoverGroup resources
func (r *FailoverGroupReconciler) watchRemoteClusters(ctx context.Context) {
	// Wait a bit for the manager to start and the clusters to connect
	time.Sleep(5 * time.Second)

	logger := log.FromContext(ctx)
	logger.Info("Starting remote cluster watcher for FailoverGroup resources")

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

				// Queue a reconcile for any FailoverGroup resources in the new cluster
				var failoverGroupList crdv1alpha1.FailoverGroupList
				if err := cl.GetClient().List(clusterCtx, &failoverGroupList); err != nil {
					if stderrors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Timeout") {
						clusterLogger.Info("Cache sync timeout when listing resources, will retry later")
						cancel()
						delete(watchCancels, name)
						continue
					}

					clusterLogger.Error(err, "Failed to list FailoverGroups in remote cluster")
					continue
				}

				for _, failoverGroup := range failoverGroupList.Items {
					logger.Info("Found FailoverGroup in remote cluster, queueing reconcile",
						"cluster", name,
						"name", failoverGroup.Name,
						"namespace", failoverGroup.Namespace)

					// Actually queue a reconcile for this resource
					r.Queue(reconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      failoverGroup.Name,
							Namespace: failoverGroup.Namespace,
						},
					}, clusterCtx)
				}

				// Set up a continuous polling mechanism for this cluster
				go r.pollClusterResources(clusterCtx, name, cl)

				// Mark this cluster as watched
				watchedClusters[name] = true
				logger.Info("Now watching cluster for FailoverGroup resources", "cluster", name)
			}

			// Clean up old clusters that we no longer need to watch
			for name := range watchedClusters {
				if _, exists := clusters[name]; !exists {
					delete(watchedClusters, name)
					if cancel, ok := watchCancels[name]; ok {
						cancel()
						delete(watchCancels, name)
					}
					logger.Info("Stopped watching cluster for FailoverGroup resources", "cluster", name)
				}
			}
		}
	}
}

// pollClusterResources polls for resources in a specific cluster periodically
func (r *FailoverGroupReconciler) pollClusterResources(ctx context.Context, clusterName string, cl cluster.Cluster) {
	logger := log.FromContext(ctx).WithValues("cluster", clusterName, "operation", "pollClusterResources")
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
			// List all FailoverGroup resources in the cluster
			var failoverGroupList crdv1alpha1.FailoverGroupList
			if err := cl.GetClient().List(ctx, &failoverGroupList); err != nil {
				// Check if context was cancelled - this is a normal shutdown case
				if stderrors.Is(err, context.Canceled) {
					logger.Info("Context cancelled during list operation")
					return
				}

				logger.Error(err, "Failed to list FailoverGroups in remote cluster")
				continue
			}

			// Check each resource
			for _, failoverGroup := range failoverGroupList.Items {
				key := fmt.Sprintf("%s/%s", failoverGroup.Namespace, failoverGroup.Name)
				// If we haven't seen this resource before or it's changed
				if lastVersion, ok := resourceVersions[key]; !ok || lastVersion != failoverGroup.ResourceVersion {
					logger.Info("Detected new or changed FailoverGroup",
						"name", failoverGroup.Name,
						"namespace", failoverGroup.Namespace,
						"newResourceVersion", failoverGroup.ResourceVersion,
						"oldResourceVersion", lastVersion)

					// Update tracked version
					resourceVersions[key] = failoverGroup.ResourceVersion

					// Queue a reconcile
					r.Queue(reconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      failoverGroup.Name,
							Namespace: failoverGroup.Namespace,
						},
					}, ctx)
				}
			}
		}
	}
}

// reconcileFailoverGroup handles the actual domain logic for a FailoverGroup resource
func (r *FailoverGroupReconciler) reconcileFailoverGroup(ctx context.Context, logger logr.Logger, failoverGroup *crdv1alpha1.FailoverGroup) error {
	// This is where you would implement the actual FailoverGroup reconciliation logic for the management cluster
	logger.Info("Reconciling FailoverGroup in management cluster")
	return nil
}

// reconcileFailoverGroupInCluster handles FailoverGroup reconciliation in a remote cluster
func (r *FailoverGroupReconciler) reconcileFailoverGroupInCluster(ctx context.Context, logger logr.Logger, failoverGroup *crdv1alpha1.FailoverGroup, clusterName string, targetClient client.Client) error {
	// This is where you would implement the FailoverGroup reconciliation logic for a remote cluster
	logger.Info("Reconciling FailoverGroup in remote cluster")
	return nil
}

// Queue enqueues a request with the proper context
func (r *FailoverGroupReconciler) Queue(request reconcile.Request, ctx context.Context) {
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
func (r *FailoverGroupReconciler) GetCluster(ctx context.Context, logger logr.Logger, clusterName string) (cluster.Cluster, error) {
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
func (r *FailoverGroupReconciler) waitForClustersReady(ctx context.Context) bool {
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
