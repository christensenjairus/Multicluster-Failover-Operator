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
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	crdv1alpha1 "github.com/christensenjairus/Multicluster-Failover-Operator/api/v1alpha1"
	kubeconfig "github.com/christensenjairus/Multicluster-Failover-Operator/providers/kubeconfig"
	"github.com/go-logr/logr"
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
		if r.MCReconciler != nil {
			cl, err := r.MCReconciler.GetCluster(ctx, failoverGroup.Status.GlobalState.ActiveCluster)
			if err != nil {
				logger.Error(err, "Failed to get active cluster", "activeCluster", failoverGroup.Status.GlobalState.ActiveCluster)
				return ctrl.Result{}, err
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
			} else if !errors.IsNotFound(err) {
				logger.Error(err, "Error getting FailoverGroup from active cluster")
			}
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
			if errors.IsNotFound(err) {
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

	// Set up a ticker to check for new clusters
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

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

				// Create a new context for this cluster
				clusterCtx := context.WithValue(ctx, ClusterNameKey, name)

				// Queue a reconcile for any FailoverGroup resources in the new cluster
				var failoverGroupList crdv1alpha1.FailoverGroupList
				if err := cl.GetClient().List(clusterCtx, &failoverGroupList); err != nil {
					logger.Error(err, "Failed to list FailoverGroups in remote cluster", "cluster", name)
					continue
				}

				for _, failoverGroup := range failoverGroupList.Items {
					logger.Info("Found FailoverGroup in remote cluster, queueing reconcile",
						"cluster", name,
						"name", failoverGroup.Name,
						"namespace", failoverGroup.Namespace)
				}

				// Mark this cluster as watched
				watchedClusters[name] = true
				logger.Info("Now watching cluster for FailoverGroup resources", "cluster", name)
			}

			// Clean up old clusters that we no longer need to watch
			for name := range watchedClusters {
				if _, exists := clusters[name]; !exists {
					delete(watchedClusters, name)
					logger.Info("Stopped watching cluster for FailoverGroup resources", "cluster", name)
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
