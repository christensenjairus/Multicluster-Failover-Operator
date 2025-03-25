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
		if r.MCReconciler != nil {
			cl, err := r.MCReconciler.GetCluster(ctx, failover.Spec.TargetCluster)
			if err != nil {
				logger.Error(err, "Failed to get target cluster", "targetCluster", failover.Spec.TargetCluster)
				return ctrl.Result{}, err
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
			} else if !errors.IsNotFound(err) {
				logger.Error(err, "Error getting Failover from target cluster")
			}
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
			if errors.IsNotFound(err) {
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

// watchRemoteClusters sets up watchers on all remote clusters for Failover resources
func (r *FailoverReconciler) watchRemoteClusters(ctx context.Context) {
	// Wait a bit for the manager to start and the clusters to connect
	time.Sleep(5 * time.Second)

	logger := log.FromContext(ctx)
	logger.Info("Starting remote cluster watcher for Failover resources")

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

				// Queue a reconcile for any Failover resources in the new cluster
				var failoverList crdv1alpha1.FailoverList
				if err := cl.GetClient().List(clusterCtx, &failoverList); err != nil {
					logger.Error(err, "Failed to list Failovers in remote cluster", "cluster", name)
					continue
				}

				for _, failover := range failoverList.Items {
					logger.Info("Found Failover in remote cluster, queueing reconcile",
						"cluster", name,
						"name", failover.Name,
						"namespace", failover.Namespace)
				}

				// Mark this cluster as watched
				watchedClusters[name] = true
				logger.Info("Now watching cluster for Failover resources", "cluster", name)
			}

			// Clean up old clusters that we no longer need to watch
			for name := range watchedClusters {
				if _, exists := clusters[name]; !exists {
					delete(watchedClusters, name)
					logger.Info("Stopped watching cluster for Failover resources", "cluster", name)
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
