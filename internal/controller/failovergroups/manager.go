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
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	kubeconfigprovider "sigs.k8s.io/multicluster-runtime/providers/kubeconfig"
)

// FailoverGroupReconciler reconciles a FailoverGroup object
type FailoverGroupReconciler struct {
	client.Client
	Manager  mcmanager.Manager
	Provider *kubeconfigprovider.Provider
	Log      logr.Logger
}

// NewFailoverGroupReconciler creates a new FailoverGroupReconciler
func NewFailoverGroupReconciler(mgr mcmanager.Manager, provider multicluster.Provider) *FailoverGroupReconciler {
	return &FailoverGroupReconciler{
		Manager:  mgr,
		Provider: provider.(*kubeconfigprovider.Provider),
		Log:      log.FromContext(context.Background()).WithName("failovergroup-reconciler"),
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
func (r *FailoverGroupReconciler) handleFailoverGroup(ctx context.Context, cl cluster.Cluster, group *crdv1alpha1.FailoverGroup) {
	log := r.Log.WithValues("failovergroup", group.Name)

	// Initialize status if needed
	if group.Status.Health == "" {
		group.Status.Health = "OK"
		group.Status.Suspended = group.Spec.Suspended
		if err := cl.GetClient().Status().Update(ctx, group); err != nil {
			log.Error(err, "Failed to update initial status")
			return
		}
	}

	// Update suspended status if changed
	if group.Status.Suspended != group.Spec.Suspended {
		group.Status.Suspended = group.Spec.Suspended
		if err := cl.GetClient().Status().Update(ctx, group); err != nil {
			log.Error(err, "Failed to update suspended status")
			return
		}
	}

	// Process workloads
	for i, workload := range group.Spec.Workloads {
		// Skip if already processed
		if i < len(group.Status.Workloads) {
			continue
		}

		// Initialize workload status
		workloadStatus := crdv1alpha1.WorkloadStatus{
			Kind:   workload.Kind,
			Name:   workload.Name,
			Health: "OK",
		}
		group.Status.Workloads = append(group.Status.Workloads, workloadStatus)

		// Update status
		if err := cl.GetClient().Status().Update(ctx, group); err != nil {
			log.Error(err, "Failed to update workload status", "workload", workload.Name)
			return
		}

		// TODO: Implement actual workload health check
		// This would involve:
		// 1. Checking pod status
		// 2. Checking volume replication status
		// 3. Checking Flux reconciliation status
	}

	// Process network resources
	for i, resource := range group.Spec.NetworkResources {
		// Skip if already processed
		if i < len(group.Status.NetworkResources) {
			continue
		}

		// Initialize network resource status
		resourceStatus := crdv1alpha1.NetworkResourceStatus{
			Kind:   resource.Kind,
			Name:   resource.Name,
			Health: "OK",
		}
		group.Status.NetworkResources = append(group.Status.NetworkResources, resourceStatus)

		// Update status
		if err := cl.GetClient().Status().Update(ctx, group); err != nil {
			log.Error(err, "Failed to update network resource status", "resource", resource.Name)
			return
		}

		// TODO: Implement actual network resource health check
		// This would involve:
		// 1. Checking resource existence
		// 2. Checking resource configuration
	}

	// Process Flux resources
	for i, resource := range group.Spec.FluxResources {
		// Skip if already processed
		if i < len(group.Status.FluxResources) {
			continue
		}

		// Initialize Flux resource status
		resourceStatus := crdv1alpha1.FluxResourceStatus{
			Kind:   resource.Kind,
			Name:   resource.Name,
			Health: "OK",
		}
		group.Status.FluxResources = append(group.Status.FluxResources, resourceStatus)

		// Update status
		if err := cl.GetClient().Status().Update(ctx, group); err != nil {
			log.Error(err, "Failed to update Flux resource status", "resource", resource.Name)
			return
		}

		// TODO: Implement actual Flux resource health check
		// This would involve:
		// 1. Checking resource existence
		// 2. Checking reconciliation status
	}

	// Update global state
	if group.Status.GlobalState.ActiveCluster == "" {
		// TODO: Implement actual global state sync
		// This would involve:
		// 1. Syncing with DynamoDB
		// 2. Updating cluster health status
		// 3. Updating active cluster information
		group.Status.GlobalState.DBSyncStatus = "Synced"
		group.Status.GlobalState.LastSyncTime = time.Now().Format(time.RFC3339)
		if err := cl.GetClient().Status().Update(ctx, group); err != nil {
			log.Error(err, "Failed to update global state")
			return
		}
	}

	// Check overall health
	allHealthy := true
	for _, workload := range group.Status.Workloads {
		if workload.Health != "OK" {
			allHealthy = false
			break
		}
	}

	if allHealthy {
		group.Status.Health = "OK"
	} else {
		group.Status.Health = "DEGRADED"
	}

	if err := cl.GetClient().Status().Update(ctx, group); err != nil {
		log.Error(err, "Failed to update overall health status")
		return
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *FailoverGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&crdv1alpha1.FailoverGroup{}).
		Complete(r)
}

//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovergroups/status,verbs=get;update;patch
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
