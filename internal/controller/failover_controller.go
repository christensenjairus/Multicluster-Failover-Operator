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
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"

	crdv1alpha1 "github.com/christensenjairus/Multicluster-Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Multicluster-Failover-Operator/internal/multicluster"
	"github.com/go-logr/logr"
)

// FailoverReconciler reconciles a Failover object
type FailoverReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Optional reference to the multicluster reconciler
	MCReconciler multicluster.MulticlusterReconcilerInterface
}

// ListClusters is a helper method that safely returns clusters or an empty map
func (r *FailoverReconciler) ListClusters() map[string]cluster.Cluster {
	if r.MCReconciler == nil {
		return map[string]cluster.Cluster{}
	}
	return r.MCReconciler.ListClusters()
}

// +kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *FailoverReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Check if we're dealing with a specific remote cluster
	var clusterName string
	var targetClient client.Client
	var crdOnly bool
	var syncMode bool

	// Check if this is a CRD-only reconciliation
	if ctx.Value("crdOnly") != nil && ctx.Value("crdOnly").(bool) {
		crdOnly = true
		logger = logger.WithValues("reconcileType", "crdOnly")
	}

	// Check if this is a sync operation
	if ctx.Value("syncMode") != nil && ctx.Value("syncMode").(bool) {
		syncMode = true
		logger = logger.WithValues("reconcileType", "sync")
	}

	// Get cluster name from context if available
	if ctx.Value("clusterName") != nil {
		clusterName = ctx.Value("clusterName").(string)
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
					syncCtx := context.WithValue(ctx, "syncMode", true)
					syncCtx = context.WithValue(syncCtx, "failover", targetFO)

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
		"syncOperation", "Failover",
		"name", failover.Name,
		"namespace", failover.Namespace,
		"targetCluster", failover.Spec.TargetCluster)

	// Get the target cluster
	if r.MCReconciler == nil {
		logger.Info("No multicluster reconciler available for synchronization")
		return
	}

	// Get the target cluster
	targetCluster, err := r.MCReconciler.GetCluster(ctx, failover.Spec.TargetCluster)
	if err != nil {
		logger.Error(err, "Failed to get target cluster", "targetCluster", failover.Spec.TargetCluster)
		return
	}

	// Validate the target cluster's client
	if targetCluster.GetClient() == nil {
		logger.Error(nil, "Target cluster client is nil", "targetCluster", failover.Spec.TargetCluster)
		return
	}

	// Create a context with the Failover for the sync operation
	syncCtx := context.WithValue(ctx, "syncMode", true)
	syncCtx = context.WithValue(syncCtx, "failover", failover)

	// Get all clusters and sync to each non-target cluster
	clusters := r.MCReconciler.ListClusters()
	if len(clusters) == 0 {
		logger.Info("No remote clusters available for failover")
		return
	}

	logger.Info("Managing failover across clusters",
		"clusterCount", len(clusters),
		"targetCluster", failover.Spec.TargetCluster)

	// Process the failover across all clusters
	for clusterName, _ := range clusters {
		clusterLogger := logger.WithValues("targetCluster", clusterName)
		isTarget := clusterName == failover.Spec.TargetCluster

		clusterLogger.Info("Processing cluster for failover",
			"isTarget", isTarget)

		// Process each FailoverGroup
		for _, groupRef := range failover.Spec.FailoverGroups {
			clusterLogger.Info("Processing FailoverGroup",
				"name", groupRef.Name,
				"namespace", groupRef.Namespace,
				"status", groupRef.Status)
		}
	}

	// Process the failover in a specific cluster
	for clusterName, _ := range clusters {
		isTarget := clusterName == failover.Spec.TargetCluster

		logger.Info("Processing failover in specific cluster",
			"cluster", clusterName,
			"isTarget", isTarget)

		// Process each FailoverGroup
		for _, groupRef := range failover.Spec.FailoverGroups {
			logger.Info("Processing FailoverGroup in cluster",
				"name", groupRef.Name,
				"namespace", groupRef.Namespace,
				"status", groupRef.Status)
		}
	}
}

// reconcileFailover handles the primary reconciliation logic for the Failover controller
func (r *FailoverReconciler) reconcileFailover(ctx context.Context, logger logr.Logger, failover *crdv1alpha1.Failover) error {
	// Get all registered clusters
	clusters := r.ListClusters()
	if len(clusters) == 0 {
		logger.Info("No remote clusters available for failover")
		return nil
	}

	logger.Info("Managing failover across clusters",
		"clusterCount", len(clusters),
		"targetCluster", failover.Spec.TargetCluster)

	// Process workloads and resources based on the Failover spec
	// This is a placeholder for actual failover logic
	for clusterName, _ := range clusters {
		clusterLogger := logger.WithValues("targetCluster", clusterName)
		isTarget := clusterName == failover.Spec.TargetCluster

		clusterLogger.Info("processing cluster for failover",
			"isTarget", isTarget)

		// Here you would implement the actual failover logic based on the target cluster
		// For example, scaling up resources in the target cluster and scaling down in others
	}

	return nil
}

// reconcileFailoverInCluster handles failover reconciliation within a specific cluster
func (r *FailoverReconciler) reconcileFailoverInCluster(ctx context.Context, logger logr.Logger, failover *crdv1alpha1.Failover, clusterName string, targetClient client.Client) error {
	isTarget := clusterName == failover.Spec.TargetCluster

	logger.Info("processing failover in specific cluster",
		"cluster", clusterName,
		"isTarget", isTarget)

	// Here you would implement cluster-specific failover logic
	// For example:
	// - If this is the target cluster, ensure all resources are scaled up
	// - If this is not the target cluster, ensure resources are scaled down or in standby

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *FailoverReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Standard controller watching the local cluster
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&crdv1alpha1.Failover{}).
		Complete(r); err != nil {
		return err
	}

	// If we have a multicluster reconciler, set up a background watcher
	if r.MCReconciler != nil {
		go r.watchRemoteClusters(context.Background())
	}

	return nil
}

// watchRemoteClusters sets up watches on remote clusters for Failover resources
func (r *FailoverReconciler) watchRemoteClusters(ctx context.Context) {
	// Give the manager time to start and the clusters to connect
	time.Sleep(5 * time.Second)

	logger := log.FromContext(ctx)
	logger.Info("Starting remote cluster watcher for Failover resources")

	// Track which clusters we've set up watches for
	watchedClusters := make(map[string]bool)

	// Set up a ticker to check for new clusters
	newClustersTicker := time.NewTicker(10 * time.Second)
	defer newClustersTicker.Stop()

	// Ticker for frequent reconciliation of CRDs
	crdReconcileTicker := time.NewTicker(5 * time.Second)
	defer crdReconcileTicker.Stop()

	// Ticker for less frequent reconciliation of managed resources
	resourceReconcileTicker := time.NewTicker(30 * time.Second)
	defer resourceReconcileTicker.Stop()

	// Keep track of the last time we did a full reconciliation for each cluster
	lastFullReconcileTime := make(map[string]time.Time)

	setupWatchForCluster := func(clusterName string, cl cluster.Cluster) {
		// Set up a watch for Failover resources in this cluster
		clusterLogger := logger.WithValues("cluster", clusterName)
		clusterLogger.Info("Setting up watch for Failover resources")

		// List current Failovers to trigger initial reconciliation
		failovers := &crdv1alpha1.FailoverList{}
		if err := cl.GetClient().List(ctx, failovers); err != nil {
			clusterLogger.Error(err, "Failed to list Failover resources during watch setup")
			return
		}

		// Process each existing Failover
		if len(failovers.Items) > 0 {
			clusterLogger.Info("Found existing Failover resources", "count", len(failovers.Items))
			for _, f := range failovers.Items {
				// Just reconcile the CRD initially, not its managed resources
				go r.reconcileCRDOnly(ctx, clusterName, f)
			}
		} else {
			clusterLogger.Info("No Failover resources found")
		}

		// Mark this cluster as watched and record the time for full reconciliation
		watchedClusters[clusterName] = true
		lastFullReconcileTime[clusterName] = time.Now()
	}

	checkClusters := func() {
		// Get all registered clusters
		clusters := r.ListClusters()
		if len(clusters) == 0 {
			return // No logging needed every time
		}

		// Check for new clusters
		currentClusters := make(map[string]bool)
		changesDetected := false

		for clusterName, cl := range clusters {
			currentClusters[clusterName] = true
			if !watchedClusters[clusterName] {
				setupWatchForCluster(clusterName, cl)
				logger.Info("Added new cluster to watch list", "cluster", clusterName, "totalWatched", len(watchedClusters))
				changesDetected = true
			}
		}

		// Check for removed clusters
		for clusterName := range watchedClusters {
			if !currentClusters[clusterName] {
				delete(watchedClusters, clusterName)
				delete(lastFullReconcileTime, clusterName)
				logger.Info("Removed cluster from watch list", "cluster", clusterName, "totalWatched", len(watchedClusters))
				changesDetected = true
			}
		}

		// If clusters were added or removed, log the full list
		if changesDetected && r.MCReconciler != nil {
			r.MCReconciler.ListClustersWithLog()
		}
	}

	// Reconcile all Failover CRDs in a cluster
	reconcileCRDs := func(clusterName string) {
		clusterLogger := logger.WithValues("cluster", clusterName)

		// Get the cluster
		cl, err := r.MCReconciler.GetCluster(ctx, clusterName)
		if err != nil {
			clusterLogger.Error(err, "Failed to get cluster")
			return
		}

		// List Failover resources on this cluster
		failovers := &crdv1alpha1.FailoverList{}
		if err := cl.GetClient().List(ctx, failovers); err != nil {
			clusterLogger.Error(err, "Failed to list Failover resources")
			return
		}

		if len(failovers.Items) > 0 {
			for _, f := range failovers.Items {
				// Only reconcile the CRD itself, not managed resources
				r.reconcileCRDOnly(ctx, clusterName, f)
			}
		}
	}

	// Do a full reconciliation including managed resources
	performFullReconciliation := func(clusterName string) {
		clusterLogger := logger.WithValues("cluster", clusterName)

		// Get the cluster
		cl, err := r.MCReconciler.GetCluster(ctx, clusterName)
		if err != nil {
			clusterLogger.Error(err, "Failed to get cluster")
			return
		}

		// List Failover resources on this cluster
		failovers := &crdv1alpha1.FailoverList{}
		if err := cl.GetClient().List(ctx, failovers); err != nil {
			clusterLogger.Error(err, "Failed to list Failover resources for full reconciliation")
			return
		}

		if len(failovers.Items) > 0 {
			clusterLogger.Info("Performing full reconciliation of Failover resources", "count", len(failovers.Items))
			for _, f := range failovers.Items {
				// Full reconciliation including managed resources
				r.Reconcile(context.WithValue(ctx, "clusterName", clusterName),
					ctrl.Request{NamespacedName: types.NamespacedName{
						Name:      f.Name,
						Namespace: f.Namespace,
					}})
			}
		}

		// Update the last full reconcile time
		lastFullReconcileTime[clusterName] = time.Now()
	}

	// Initial check for clusters
	checkClusters()

	for {
		select {
		case <-ctx.Done():
			return

		case <-newClustersTicker.C:
			// Check for new/removed clusters
			checkClusters()

		case <-crdReconcileTicker.C:
			// Frequently reconcile just the CRDs
			for clusterName := range watchedClusters {
				reconcileCRDs(clusterName)
			}

		case <-resourceReconcileTicker.C:
			// Less frequently reconcile the managed resources
			for clusterName := range watchedClusters {
				performFullReconciliation(clusterName)
			}
		}
	}
}

// reconcileCRDOnly reconciles just the Failover CRD status, not its managed resources
func (r *FailoverReconciler) reconcileCRDOnly(ctx context.Context, clusterName string, f crdv1alpha1.Failover) {
	ctx = context.WithValue(ctx, "crdOnly", true)
	ctx = context.WithValue(ctx, "clusterName", clusterName)

	r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{
		Name:      f.Name,
		Namespace: f.Namespace,
	}})
}
