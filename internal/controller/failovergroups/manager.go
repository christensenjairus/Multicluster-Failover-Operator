package failovergroups

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"

	crdv1alpha1 "github.com/christensenjairus/Multicluster-Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Multicluster-Failover-Operator/internal/controller"
	"github.com/christensenjairus/Multicluster-Failover-Operator/internal/mirror"
	"k8s.io/apimachinery/pkg/api/errors"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	kubeconfigprovider "sigs.k8s.io/multicluster-runtime/providers/kubeconfig"
)

// FailoverGroupReconciler reconciles a FailoverGroup object
type FailoverGroupReconciler struct {
	client.Client
	Manager  mcmanager.Manager
	Provider *kubeconfigprovider.Provider
	Log      logr.Logger
	Mirror   *mirror.ResourceMirror
}

// NewFailoverGroupReconciler creates a new FailoverGroupReconciler
func NewFailoverGroupReconciler(mgr mcmanager.Manager) *FailoverGroupReconciler {
	return &FailoverGroupReconciler{
		Manager:  mgr,
		Provider: mgr.GetProvider().(*kubeconfigprovider.Provider),
		Log:      log.FromContext(context.Background()).WithName("failovergroup-reconciler"),
		Mirror:   mirror.NewResourceMirror(mgr.GetProvider().(*kubeconfigprovider.Provider)),
	}
}

// Start implements Runnable
func (r *FailoverGroupReconciler) Start(ctx context.Context) error {
	log := r.Log
	log.Info("Starting failover group reconciler")

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
func (r *FailoverGroupReconciler) Engage(ctx context.Context, clusterName string, cl cluster.Cluster) error {
	log := r.Log.WithValues("cluster", clusterName)
	log.Info("Setting up watch for new cluster")

	return r.setupWatch(ctx, cl, clusterName)
}

// setupWatch sets up a watch for failover groups in a cluster
func (r *FailoverGroupReconciler) setupWatch(ctx context.Context, cl cluster.Cluster, clusterName string) error {
	log := r.Log.WithValues("cluster", clusterName)

	// Initial list of failover groups
	var failoverGroups crdv1alpha1.FailoverGroupList
	if err := cl.GetClient().List(ctx, &failoverGroups, &client.ListOptions{}); err != nil {
		return err
	}

	log.Info("Initial failover group list",
		"count", len(failoverGroups.Items))
	for _, group := range failoverGroups.Items {
		log.Info("Failover group",
			"name", group.Name,
			"health", group.Status.Health)
	}

	// Set up mirroring for FailoverGroups
	if err := controller.SetupResourceMirroring(ctx, r.Provider, cl, clusterName, &crdv1alpha1.FailoverGroup{}); err != nil {
		log.Error(err, "Failed to set up resource mirroring")
		return err
	}

	// Get the informer for failover groups
	informer, err := cl.GetCache().GetInformer(ctx, &crdv1alpha1.FailoverGroup{})
	if err != nil {
		return err
	}

	// Add event handlers
	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			group := obj.(*crdv1alpha1.FailoverGroup)
			log.Info("Failover group added",
				"name", group.Name,
				"health", group.Status.Health)
			r.handleFailoverGroup(ctx, cl, group)
		},
		UpdateFunc: func(old, new interface{}) {
			oldGroup := old.(*crdv1alpha1.FailoverGroup)
			newGroup := new.(*crdv1alpha1.FailoverGroup)
			if oldGroup.Status.Health != newGroup.Status.Health {
				log.Info("Failover group health changed",
					"name", newGroup.Name,
					"oldHealth", oldGroup.Status.Health,
					"newHealth", newGroup.Status.Health)
				r.handleFailoverGroup(ctx, cl, newGroup)
			}
		},
		DeleteFunc: func(obj interface{}) {
			group, ok := obj.(*crdv1alpha1.FailoverGroup)
			if !ok {
				// If the object is a DeletedFinalStateUnknown, try to get the group from it
				if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
					group, ok = tombstone.Obj.(*crdv1alpha1.FailoverGroup)
					if !ok {
						log.Error(nil, "Error decoding failover group from tombstone")
						return
					}
				} else {
					log.Error(nil, "Error decoding failover group")
					return
				}
			}
			log.Info("Failover group deleted", "name", group.Name)
		},
	})

	return err
}

// handleFailoverGroup processes a failover group event
func (r *FailoverGroupReconciler) handleFailoverGroup(ctx context.Context, cl cluster.Cluster, failoverGroup *crdv1alpha1.FailoverGroup) {
	log := r.Log.WithValues("failovergroup", failoverGroup.Name)

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

	// Only process if this is the active cluster
	if failoverGroup.Status.GlobalState.ActiveCluster != "" && failoverGroup.Status.GlobalState.ActiveCluster != clusterName {
		log.V(2).Info("Skipping processing - not the active cluster",
			"activeCluster", failoverGroup.Status.GlobalState.ActiveCluster,
			"currentCluster", clusterName)
		return
	}

	// Initialize status if needed
	if failoverGroup.Status.Health == "" {
		failoverGroup.Status.Health = "OK"
		failoverGroup.Status.Suspended = failoverGroup.Spec.Suspended

		// Set the active cluster to the cluster where the failover group is first created
		if failoverGroup.Status.GlobalState.ActiveCluster == "" {
			failoverGroup.Status.GlobalState.ActiveCluster = clusterName
			failoverGroup.Status.GlobalState.Clusters = []crdv1alpha1.ClusterInfo{
				{
					Name:   clusterName,
					Role:   "PRIMARY",
					Health: "UNKNOWN",
				},
			}
		}

		// Update the status on the active cluster
		if err := cl.GetClient().Status().Update(ctx, failoverGroup); err != nil {
			if !errors.IsConflict(err) {
				log.V(2).Error(err, "Failed to update initial status")
				return
			}
			// If there's a conflict, get the latest version and try again
			if err := cl.GetClient().Get(ctx, client.ObjectKey{
				Namespace: failoverGroup.Namespace,
				Name:      failoverGroup.Name,
			}, failoverGroup); err != nil {
				log.V(2).Error(err, "Failed to get latest version after conflict")
				return
			}
			// Reapply our changes
			failoverGroup.Status.Health = "OK"
			failoverGroup.Status.Suspended = failoverGroup.Spec.Suspended
			if failoverGroup.Status.GlobalState.ActiveCluster == "" {
				failoverGroup.Status.GlobalState.ActiveCluster = clusterName
				failoverGroup.Status.GlobalState.Clusters = []crdv1alpha1.ClusterInfo{
					{
						Name:   clusterName,
						Role:   "PRIMARY",
						Health: "OK",
					},
				}
			}
			if err := cl.GetClient().Status().Update(ctx, failoverGroup); err != nil {
				log.V(2).Error(err, "Failed to update initial status after conflict resolution")
				return
			}
		}

		// Only mirror after successful update
		// Create a deep copy for mirroring to preserve the source cluster's state
		mirrorObj := failoverGroup.DeepCopy()
		if err := r.Mirror.MirrorObject(ctx, clusterName, mirrorObj); err != nil {
			log.V(2).Error(err, "Failed to mirror initial status")
		}
	}

	// Update suspended status if changed
	if failoverGroup.Status.Suspended != failoverGroup.Spec.Suspended {
		failoverGroup.Status.Suspended = failoverGroup.Spec.Suspended
		if err := cl.GetClient().Status().Update(ctx, failoverGroup); err != nil {
			if !errors.IsConflict(err) {
				log.V(2).Error(err, "Failed to update suspended status")
			}
			return
		}
		// Mirror the status update to other clusters
		if err := r.Mirror.MirrorObject(ctx, clusterName, failoverGroup); err != nil {
			log.V(2).Error(err, "Failed to mirror suspended status")
		}
	}

	// Process workloads
	for i, workload := range failoverGroup.Spec.Workloads {
		// Skip if already processed
		if i < len(failoverGroup.Status.Workloads) {
			continue
		}

		// Initialize workload status
		workloadStatus := crdv1alpha1.WorkloadStatus{
			Kind:   workload.Kind,
			Name:   workload.Name,
			Health: "OK",
		}
		failoverGroup.Status.Workloads = append(failoverGroup.Status.Workloads, workloadStatus)

		// Update status
		if err := cl.GetClient().Status().Update(ctx, failoverGroup); err != nil {
			if !errors.IsConflict(err) {
				log.V(2).Error(err, "Failed to update workload status", "workload", workload.Name)
			}
			return
		}
		// Mirror the status update to other clusters
		if err := r.Mirror.MirrorObject(ctx, clusterName, failoverGroup); err != nil {
			log.V(2).Error(err, "Failed to mirror workload status", "workload", workload.Name)
		}

		// TODO: Implement actual workload health check
		// This would involve:
		// 1. Checking pod status
		// 2. Checking volume replication status
		// 3. Checking Flux reconciliation status
	}

	// Process network resources
	for i, resource := range failoverGroup.Spec.NetworkResources {
		// Skip if already processed
		if i < len(failoverGroup.Status.NetworkResources) {
			continue
		}

		// Initialize network resource status
		resourceStatus := crdv1alpha1.NetworkResourceStatus{
			Kind:   resource.Kind,
			Name:   resource.Name,
			Health: "OK",
		}
		failoverGroup.Status.NetworkResources = append(failoverGroup.Status.NetworkResources, resourceStatus)

		// Update status
		if err := cl.GetClient().Status().Update(ctx, failoverGroup); err != nil {
			if !errors.IsConflict(err) {
				log.V(2).Error(err, "Failed to update network resource status", "resource", resource.Name)
			}
			return
		}
		// Mirror the status update to other clusters
		if err := r.Mirror.MirrorObject(ctx, clusterName, failoverGroup); err != nil {
			log.V(2).Error(err, "Failed to mirror network resource status", "resource", resource.Name)
		}

		// TODO: Implement actual network resource health check
		// This would involve:
		// 1. Checking resource existence
		// 2. Checking resource configuration
	}

	// Process Flux resources
	for i, resource := range failoverGroup.Spec.FluxResources {
		// Skip if already processed
		if i < len(failoverGroup.Status.FluxResources) {
			continue
		}

		// Initialize Flux resource status
		resourceStatus := crdv1alpha1.FluxResourceStatus{
			Kind:   resource.Kind,
			Name:   resource.Name,
			Health: "OK",
		}
		failoverGroup.Status.FluxResources = append(failoverGroup.Status.FluxResources, resourceStatus)

		// Update status
		if err := cl.GetClient().Status().Update(ctx, failoverGroup); err != nil {
			if !errors.IsConflict(err) {
				log.V(2).Error(err, "Failed to update Flux resource status", "resource", resource.Name)
			}
			return
		}
		// Mirror the status update to other clusters
		if err := r.Mirror.MirrorObject(ctx, clusterName, failoverGroup); err != nil {
			log.V(2).Error(err, "Failed to mirror Flux resource status", "resource", resource.Name)
		}

		// TODO: Implement actual Flux resource health check
		// This would involve:
		// 1. Checking resource existence
		// 2. Checking reconciliation status
	}

	// Update global state
	if failoverGroup.Status.GlobalState.ActiveCluster == "" {
		// TODO: Implement actual global state sync
		// This would involve:
		// 1. Syncing with DynamoDB
		// 2. Updating cluster health status
		// 3. Updating active cluster information
		failoverGroup.Status.GlobalState.LastSyncTime = time.Now().Format(time.RFC3339)
		if err := cl.GetClient().Status().Update(ctx, failoverGroup); err != nil {
			if !errors.IsConflict(err) {
				log.V(2).Error(err, "Failed to update global state")
			}
			return
		}
		// Mirror the status update to other clusters
		if err := r.Mirror.MirrorObject(ctx, clusterName, failoverGroup); err != nil {
			log.V(2).Error(err, "Failed to mirror global state")
		}
	}

	// Check overall health
	allHealthy := true
	for _, workload := range failoverGroup.Status.Workloads {
		if workload.Health != "OK" {
			allHealthy = false
			break
		}
	}

	if allHealthy {
		failoverGroup.Status.Health = "OK"
	} else {
		failoverGroup.Status.Health = "DEGRADED"
	}

	if err := cl.GetClient().Status().Update(ctx, failoverGroup); err != nil {
		if !errors.IsConflict(err) {
			log.V(2).Error(err, "Failed to update overall health status")
		}
	}
	// Mirror the final status update to other clusters
	if err := r.Mirror.MirrorObject(ctx, clusterName, failoverGroup); err != nil {
		log.V(2).Error(err, "Failed to mirror final health status")
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *FailoverGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&crdv1alpha1.FailoverGroup{}).
		Complete(r)
}

//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups/status,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *FailoverGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("failovergroup", req.NamespacedName)
	failoverGroup := &crdv1alpha1.FailoverGroup{}
	if err := r.Get(ctx, req.NamespacedName, failoverGroup); err != nil {
		log.Error(err, "Failed to get failover group")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Get the cluster this failover group is in
	clusterName := req.NamespacedName.Namespace
	cl, err := r.Provider.Get(ctx, clusterName)
	if err != nil {
		log.Error(err, "Failed to get cluster", "cluster", clusterName)
		return ctrl.Result{}, err
	}

	// Process the failover group
	r.handleFailoverGroup(ctx, cl, failoverGroup)

	return ctrl.Result{}, nil
}
