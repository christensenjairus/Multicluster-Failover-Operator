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
	"github.com/christensenjairus/Multicluster-Failover-Operator/internal/constants"
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
}

// NewFailoverGroupReconciler creates a new FailoverGroupReconciler
func NewFailoverGroupReconciler(mgr mcmanager.Manager) *FailoverGroupReconciler {
	return &FailoverGroupReconciler{
		Manager:  mgr,
		Provider: mgr.GetProvider().(*kubeconfigprovider.Provider),
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

	// Check if this is a new FailoverGroup (no sot cluster annotation)
	if failoverGroup.Annotations == nil || failoverGroup.Annotations[constants.SotClusterAnnotation] == "" {
		// This is a new FailoverGroup, set the sot cluster annotation
		if failoverGroup.Annotations == nil {
			failoverGroup.Annotations = make(map[string]string)
		}
		failoverGroup.Annotations[constants.SotClusterAnnotation] = clusterName
		if err := cl.GetClient().Update(ctx, failoverGroup); err != nil {
			log.Error(err, "Failed to set sot cluster annotation")
			return
		}
		log.Info("Set sot cluster annotation", "cluster", clusterName)
	}

	// Only process if this is the source of truth cluster
	if sotCluster := failoverGroup.Annotations[constants.SotClusterAnnotation]; sotCluster != clusterName {
		log.V(2).Info("Skipping processing - not the source of truth cluster",
			"sotCluster", sotCluster,
			"currentCluster", clusterName)
		return
	}

	// Initialize status if needed
	if failoverGroup.Status.Health == "" {
		failoverGroup.Status.Health = "OK"
		failoverGroup.Status.LastHeartbeat = time.Now().Format(time.RFC3339)

		// Set the active cluster to the cluster where the failover group is first created
		if failoverGroup.Status.ActiveCluster == "" {
			failoverGroup.Status.ActiveCluster = clusterName
		}

		// Update the status
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
			failoverGroup.Status.LastHeartbeat = time.Now().Format(time.RFC3339)
			if failoverGroup.Status.ActiveCluster == "" {
				failoverGroup.Status.ActiveCluster = clusterName
			}
			if err := cl.GetClient().Status().Update(ctx, failoverGroup); err != nil {
				log.V(2).Error(err, "Failed to update initial status after conflict resolution")
				return
			}
		}
	}

	// Update heartbeat if needed
	if failoverGroup.Spec.HeartbeatInterval != "" {
		// TODO: Implement heartbeat interval check and update
		// This would involve:
		// 1. Parsing the heartbeat interval
		// 2. Checking if enough time has passed
		// 3. Updating LastHeartbeat if needed
	}

	// Check overall health
	// TODO: Implement actual health checks for:
	// 1. Workloads
	// 2. Network resources
	// 3. Flux resources
	allHealthy := true // Placeholder until actual health checks are implemented

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
