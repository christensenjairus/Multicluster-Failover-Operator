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
	kubeconfig "github.com/christensenjairus/Multicluster-Failover-Operator/providers/kubeconfig"
	"github.com/go-logr/logr"
	// We're no longer using the multicluster-runtime directly in the controllers
	// The main.go will handle setting up the controllers with mcbuilder
)

// FailoverGroupReconciler reconciles a FailoverGroup object
type FailoverGroupReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Optional reference to the multicluster reconciler
	MCReconciler *kubeconfig.MulticlusterReconciler
}

// ListClusters is a helper method that safely returns clusters or an empty map
func (r *FailoverGroupReconciler) ListClusters() map[string]cluster.Cluster {
	if r.MCReconciler == nil {
		return map[string]cluster.Cluster{}
	}
	return r.MCReconciler.ListClusters()
}

// +kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *FailoverGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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
		clusters := r.ListClusters()
		if cl, ok := clusters[clusterName]; ok && cl != nil {
			targetClient = cl.GetClient()
			logger.Info("Using remote cluster client", "cluster", clusterName)
		} else {
			logger.Error(nil, "Remote cluster not found or nil", "cluster", clusterName)
			return ctrl.Result{}, nil
		}
	} else {
		// Use the regular client for local cluster
		targetClient = r.Client
		logger.Info("Using local cluster client")
	}

	// Get the FailoverGroup resource
	failoverGroup := &crdv1alpha1.FailoverGroup{}

	// Get the FailoverGroup from the local/target cluster
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
		"namespace", failoverGroup.Namespace,
		"suspended", failoverGroup.Spec.Suspended)

	// Determine the active/primary cluster
	activeCluster := determineActiveCluster(failoverGroup)
	if activeCluster != "" {
		logger.Info("Active cluster determined", "activeCluster", activeCluster)
	} else {
		logger.Info("No active cluster could be determined from FailoverGroup status")
	}

	// If we are not the active cluster and not in sync mode, check if we need to fetch the latest version from the active cluster
	if clusterName != activeCluster && !syncMode && activeCluster != "" {
		// Get the client for the active cluster
		clusters := r.ListClusters()
		if cl, ok := clusters[activeCluster]; ok && cl != nil {
			activeClient := cl.GetClient()

			// Get the FailoverGroup from the active cluster
			activeFG := &crdv1alpha1.FailoverGroup{}
			err := activeClient.Get(ctx, req.NamespacedName, activeFG)
			if err == nil {
				// We found the resource in the active cluster, check if we need to update our local copy
				if shouldUpdateFromActive(failoverGroup, activeFG) {
					logger.Info("Updating local FailoverGroup from active cluster",
						"activeCluster", activeCluster,
						"localResourceVersion", failoverGroup.ResourceVersion,
						"activeResourceVersion", activeFG.ResourceVersion)

					// Update our local copy with changes from the active cluster
					syncCtx := context.WithValue(ctx, "syncMode", true)
					syncCtx = context.WithValue(syncCtx, "failoverGroup", activeFG)

					// Call Reconcile with sync context
					return r.Reconcile(syncCtx, req)
				}
			} else if !errors.IsNotFound(err) {
				logger.Error(err, "Error getting FailoverGroup from active cluster")
			}
		}
	}

	// If we're the active cluster, and there's a change, sync to other clusters
	if (clusterName == activeCluster || (clusterName == "" && activeCluster == "")) && !syncMode {
		// This cluster is the active one (or we're in the management cluster and no active cluster is specified)
		// Now, let's synchronize this to all other clusters
		go r.syncToOtherClusters(ctx, failoverGroup, activeCluster)
	}

	// CRD-only reconciliation ends here if requested
	if crdOnly {
		logger.Info("Performing CRD-only reconciliation, skipping managed resources")
		return ctrl.Result{}, nil
	}

	// Continue with reconciliation
	if clusterName == "" {
		// This is the management cluster
		r.reconcileWorkloads(ctx, logger, failoverGroup)
	} else {
		// This is a remote cluster
		r.reconcileWorkloadsInCluster(ctx, logger, failoverGroup, clusterName, targetClient)
	}

	return ctrl.Result{}, nil
}

// determineActiveCluster determines which cluster is currently active/primary based on the FailoverGroup status
func determineActiveCluster(failoverGroup *crdv1alpha1.FailoverGroup) string {
	// First check if the GlobalState has an explicitly set ActiveCluster
	if failoverGroup.Status.GlobalState.ActiveCluster != "" {
		return failoverGroup.Status.GlobalState.ActiveCluster
	}

	// Otherwise, look for a cluster with the PRIMARY role in the clusters list
	for _, cluster := range failoverGroup.Status.GlobalState.Clusters {
		if cluster.Role == "PRIMARY" {
			return cluster.Name
		}
	}

	// If nothing is explicitly set, return empty string
	return ""
}

// shouldUpdateFromActive determines if the local FailoverGroup should be updated from the active cluster's version
func shouldUpdateFromActive(local, active *crdv1alpha1.FailoverGroup) bool {
	// Compare resource versions if they're in the format we expect (numerical)
	// Otherwise, we'll go with creation/modification timestamps
	// In a real implementation, you might want a more sophisticated comparison

	// Here we're assuming the active cluster version should always be used
	// You could implement more complex logic here
	return true
}

// syncToOtherClusters synchronizes a FailoverGroup from the active cluster to all other clusters
func (r *FailoverGroupReconciler) syncToOtherClusters(ctx context.Context, failoverGroup *crdv1alpha1.FailoverGroup, activeCluster string) {
	logger := log.FromContext(ctx).WithValues(
		"syncOperation", "FailoverGroup",
		"name", failoverGroup.Name,
		"namespace", failoverGroup.Namespace,
		"activeCluster", activeCluster)

	// Get all registered remote clusters
	clusters := r.ListClusters()
	if len(clusters) == 0 {
		logger.Info("No remote clusters available for synchronization")
		return
	}

	logger.Info("Syncing FailoverGroup from active cluster to all other clusters", "clusterCount", len(clusters))

	// Create a context with the FailoverGroup for the sync operation
	syncCtx := context.WithValue(ctx, "syncMode", true)
	syncCtx = context.WithValue(syncCtx, "failoverGroup", failoverGroup)

	// Synchronize to each non-active remote cluster
	for clusterName, _ := range clusters {
		// Skip the active cluster, no need to sync to itself
		if clusterName == activeCluster {
			continue
		}

		clusterLogger := logger.WithValues("targetCluster", clusterName)
		clusterLogger.Info("Synchronizing FailoverGroup to non-active cluster")

		// Call Reconcile with the sync context for each remote cluster
		clusterCtx := context.WithValue(syncCtx, "clusterName", clusterName)
		_, err := r.Reconcile(clusterCtx, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      failoverGroup.Name,
				Namespace: failoverGroup.Namespace,
			},
		})

		if err != nil {
			clusterLogger.Error(err, "Failed to synchronize FailoverGroup to non-active cluster")
		}
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *FailoverGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Standard controller watching the local cluster
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
	// Give the manager time to start and the clusters to connect
	time.Sleep(5 * time.Second)

	logger := log.FromContext(ctx)
	logger.Info("Starting remote cluster watcher for FailoverGroup resources")

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
		if cl == nil || cl.GetClient() == nil {
			logger.Error(nil, "Cluster client is nil", "cluster", clusterName)
			return
		}

		// Set up a watch for FailoverGroup resources in this cluster
		clusterLogger := logger.WithValues("cluster", clusterName)
		clusterLogger.Info("Setting up watch for FailoverGroup resources")

		// List current FailoverGroups to trigger initial reconciliation
		failoverGroups := &crdv1alpha1.FailoverGroupList{}
		if err := cl.GetClient().List(ctx, failoverGroups); err != nil {
			clusterLogger.Error(err, "Failed to list FailoverGroup resources during watch setup")
			return
		}

		// Process each existing FailoverGroup
		if len(failoverGroups.Items) > 0 {
			clusterLogger.Info("Found existing FailoverGroup resources", "count", len(failoverGroups.Items))
			for _, fg := range failoverGroups.Items {
				// Just reconcile the CRD initially, not its managed resources
				go r.reconcileCRDOnly(ctx, clusterName, fg)
			}
		} else {
			clusterLogger.Info("No FailoverGroup resources found")
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

	// Reconcile all FailoverGroup CRDs in a cluster
	reconcileCRDs := func(clusterName string, cl cluster.Cluster) {
		clusterLogger := logger.WithValues("cluster", clusterName)

		// List FailoverGroup resources on this cluster
		failoverGroups := &crdv1alpha1.FailoverGroupList{}
		if err := cl.GetClient().List(ctx, failoverGroups); err != nil {
			clusterLogger.Error(err, "Failed to list FailoverGroup resources")
			return
		}

		if len(failoverGroups.Items) > 0 {
			for _, fg := range failoverGroups.Items {
				// Only reconcile the CRD itself, not managed resources
				r.reconcileCRDOnly(ctx, clusterName, fg)
			}
		}
	}

	// Do a full reconciliation including managed resources
	performFullReconciliation := func(clusterName string, cl cluster.Cluster) {
		clusterLogger := logger.WithValues("cluster", clusterName)

		// List FailoverGroup resources on this cluster
		failoverGroups := &crdv1alpha1.FailoverGroupList{}
		if err := cl.GetClient().List(ctx, failoverGroups); err != nil {
			clusterLogger.Error(err, "Failed to list FailoverGroup resources for full reconciliation")
			return
		}

		if len(failoverGroups.Items) > 0 {
			clusterLogger.Info("Performing full reconciliation of FailoverGroup resources", "count", len(failoverGroups.Items))
			for _, fg := range failoverGroups.Items {
				// Full reconciliation including managed resources
				r.Reconcile(context.WithValue(ctx, "clusterName", clusterName),
					ctrl.Request{NamespacedName: types.NamespacedName{
						Name:      fg.Name,
						Namespace: fg.Namespace,
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
			for clusterName, cl := range r.ListClusters() {
				if watchedClusters[clusterName] && cl != nil && cl.GetClient() != nil {
					reconcileCRDs(clusterName, cl)
				}
			}

		case <-resourceReconcileTicker.C:
			// Less frequently reconcile the managed resources
			for clusterName, cl := range r.ListClusters() {
				if watchedClusters[clusterName] && cl != nil && cl.GetClient() != nil {
					performFullReconciliation(clusterName, cl)
				}
			}
		}
	}
}

// reconcileCRDOnly reconciles just the FailoverGroup CRD status, not its managed resources
func (r *FailoverGroupReconciler) reconcileCRDOnly(ctx context.Context, clusterName string, fg crdv1alpha1.FailoverGroup) {
	ctx = context.WithValue(ctx, "crdOnly", true)
	ctx = context.WithValue(ctx, "clusterName", clusterName)

	r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{
		Name:      fg.Name,
		Namespace: fg.Namespace,
	}})
}

// reconcileWorkloads handles the reconciliation of workload resources across all clusters
func (r *FailoverGroupReconciler) reconcileWorkloads(ctx context.Context, logger logr.Logger, failoverGroup *crdv1alpha1.FailoverGroup) {
	// Get all registered clusters
	clusters := r.ListClusters()
	logger.Info("Managing failover across clusters", "clusterCount", len(clusters))

	// Process the failover group across all clusters
	for remoteName, _ := range clusters {
		clusterLogger := logger.WithValues("targetCluster", remoteName)
		clusterLogger.Info("Processing FailoverGroup on remote cluster")

		// Process workloads
		for _, workload := range failoverGroup.Spec.Workloads {
			clusterLogger.Info("Would check/create workload",
				"kind", workload.Kind,
				"name", workload.Name)
		}

		// Process network resources
		for _, network := range failoverGroup.Spec.NetworkResources {
			clusterLogger.Info("Would check/create network resource",
				"kind", network.Kind,
				"name", network.Name)
		}

		// Process Flux resources if defined
		for _, flux := range failoverGroup.Spec.FluxResources {
			clusterLogger.Info("Would check/create Flux resource",
				"kind", flux.Kind,
				"name", flux.Name,
				"triggerReconcile", flux.TriggerReconcile)
		}
	}
}

// reconcileWorkloadsInCluster handles the reconciliation of workload resources in a specific cluster
func (r *FailoverGroupReconciler) reconcileWorkloadsInCluster(ctx context.Context, logger logr.Logger, failoverGroup *crdv1alpha1.FailoverGroup, clusterName string, targetClient client.Client) {
	logger.Info("Processing FailoverGroup on specific remote cluster", "cluster", clusterName)

	// Process workloads
	for _, workload := range failoverGroup.Spec.Workloads {
		logger.Info("Would check/create workload on remote cluster",
			"kind", workload.Kind,
			"name", workload.Name)
	}

	// Process network resources
	for _, network := range failoverGroup.Spec.NetworkResources {
		logger.Info("Would check/create network resource on remote cluster",
			"kind", network.Kind,
			"name", network.Name)
	}

	// Process Flux resources if defined
	for _, flux := range failoverGroup.Spec.FluxResources {
		logger.Info("Would check/create Flux resource on remote cluster",
			"kind", flux.Kind,
			"name", flux.Name,
			"triggerReconcile", flux.TriggerReconcile)
	}
}
