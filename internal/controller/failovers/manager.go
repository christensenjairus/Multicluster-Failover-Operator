package failovers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"

	crdv1alpha1 "github.com/christensenjairus/Multicluster-Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Multicluster-Failover-Operator/internal/controller"
	"github.com/christensenjairus/Multicluster-Failover-Operator/internal/controller/failovers/statemachine"
	"k8s.io/apimachinery/pkg/api/errors"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	kubeconfigprovider "sigs.k8s.io/multicluster-runtime/providers/kubeconfig"
)

// FailoverReconciler reconciles a Failover object
type FailoverReconciler struct {
	client.Client
	Manager  mcmanager.Manager
	Provider *kubeconfigprovider.Provider
	Log      logr.Logger
}

// NewFailoverReconciler creates a new FailoverReconciler
func NewFailoverReconciler(mgr mcmanager.Manager) *FailoverReconciler {
	return &FailoverReconciler{
		Manager:  mgr,
		Provider: mgr.GetProvider().(*kubeconfigprovider.Provider),
		Log:      log.FromContext(context.Background()).WithName("failover-reconciler"),
	}
}

// Start implements Runnable
func (r *FailoverReconciler) Start(ctx context.Context) error {
	log := r.Log
	log.Info("Starting failover reconciler")

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
func (r *FailoverReconciler) Engage(ctx context.Context, clusterName string, cl cluster.Cluster) error {
	log := r.Log.WithValues("cluster", clusterName)
	log.Info("Setting up watch for new cluster")

	return r.setupWatch(ctx, cl, clusterName)
}

// setupWatch sets up a watch for failovers in a cluster
func (r *FailoverReconciler) setupWatch(ctx context.Context, cl cluster.Cluster, clusterName string) error {
	log := r.Log.WithValues("cluster", clusterName)

	// Initial list of failovers
	var failovers crdv1alpha1.FailoverList
	if err := cl.GetClient().List(ctx, &failovers, &client.ListOptions{}); err != nil {
		return err
	}

	log.Info("Initial failover list",
		"count", len(failovers.Items))
	for _, failover := range failovers.Items {
		log.Info("Failover",
			"name", failover.Name,
			"status", failover.Status.Status)
	}

	// Set up mirroring for Failovers
	if err := controller.SetupResourceMirroring(ctx, r.Provider, cl, clusterName, &crdv1alpha1.Failover{}); err != nil {
		log.Error(err, "Failed to set up resource mirroring")
		return err
	}

	// Get the informer for failovers
	informer, err := cl.GetCache().GetInformer(ctx, &crdv1alpha1.Failover{})
	if err != nil {
		return err
	}

	// Add event handlers
	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			failover := obj.(*crdv1alpha1.Failover)
			log.Info("Failover added",
				"name", failover.Name,
				"status", failover.Status.Status)
			r.handleFailover(ctx, cl, failover)
		},
		UpdateFunc: func(old, new interface{}) {
			oldFailover := old.(*crdv1alpha1.Failover)
			newFailover := new.(*crdv1alpha1.Failover)
			if oldFailover.Status.Status != newFailover.Status.Status {
				log.Info("Failover status changed",
					"name", newFailover.Name,
					"oldStatus", oldFailover.Status.Status,
					"newStatus", newFailover.Status.Status)
				r.handleFailover(ctx, cl, newFailover)
			}
		},
		DeleteFunc: func(obj interface{}) {
			failover, ok := obj.(*crdv1alpha1.Failover)
			if !ok {
				// If the object is a DeletedFinalStateUnknown, try to get the failover from it
				if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
					failover, ok = tombstone.Obj.(*crdv1alpha1.Failover)
					if !ok {
						log.Error(nil, "Error decoding failover from tombstone")
						return
					}
				} else {
					log.Error(nil, "Error decoding failover")
					return
				}
			}
			log.Info("Failover deleted", "name", failover.Name)
		},
	})

	return err
}

// handleFailover processes a failover event
func (r *FailoverReconciler) handleFailover(ctx context.Context, cl cluster.Cluster, failover *crdv1alpha1.Failover) {
	log := r.Log.WithValues("failover", failover.Name)

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

	// Get the failover group to check if this is the active cluster
	var failoverGroup crdv1alpha1.FailoverGroup
	if err := cl.GetClient().Get(ctx, client.ObjectKey{
		Namespace: failover.Spec.FailoverGroups[0].Namespace,
		Name:      failover.Spec.FailoverGroups[0].Name,
	}, &failoverGroup); err != nil {
		log.Error(err, "Failed to get failover group")
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
	if failover.Status.Status == "" {
		if err := r.updateFailoverStatus(ctx, cl, failover, "IN_PROGRESS", "INITIALIZING", "Starting failover process"); err != nil {
			return
		}
	}

	// If the failover is already completed, don't process it again
	if failover.Status.Status == "SUCCESS" {
		log.Info("Failover already completed successfully, skipping processing")
		return
	}

	// Get all clusters
	clusters := make(map[string]client.Client)
	for clusterName, cluster := range r.Provider.ListClusters() {
		clusters[clusterName] = cluster.GetClient()
	}

	// Process each failover group
	for _, groupRef := range failover.Spec.FailoverGroups {
		// Get the failover group from the source cluster
		var failoverGroup crdv1alpha1.FailoverGroup
		if err := cl.GetClient().Get(ctx, client.ObjectKey{
			Namespace: groupRef.Namespace,
			Name:      groupRef.Name,
		}, &failoverGroup); err != nil {
			log.V(2).Error(err, "Failed to get failover group", "group", groupRef.Name)
			continue
		}

		// Create and execute state machine for this failover group
		sm := statemachine.NewStateMachine(failover, &failoverGroup, clusters, log)

		// Execute state machine until completion or error
		for {
			if err := sm.Execute(ctx); err != nil {
				log.V(2).Error(err, "Failed to execute state machine for failover group", "group", groupRef.Name)
				if err := r.updateFailoverStatus(ctx, cl, failover, "FAILED", "ERROR", err.Error()); err != nil {
					log.V(2).Error(err, "Failed to update error status")
				}
				break
			}

			// Check if we've reached the Complete state
			if sm.GetCurrentState().Name() == "Complete" {
				log.Info("State machine completed successfully", "group", groupRef.Name)
				if err := r.updateFailoverStatus(ctx, cl, failover, "SUCCESS", "COMPLETED", "Failover completed successfully"); err != nil {
					log.V(2).Error(err, "Failed to update success status")
				}
				break
			}
		}
	}
}

// updateFailoverStatus updates the status of the Failover resource
func (r *FailoverReconciler) updateFailoverStatus(ctx context.Context, cl cluster.Cluster, failover *crdv1alpha1.Failover, status, state, message string) error {
	// Get the latest version of the failover
	if err := cl.GetClient().Get(ctx, client.ObjectKey{
		Namespace: failover.Namespace,
		Name:      failover.Name,
	}, failover); err != nil {
		return fmt.Errorf("failed to get latest failover: %w", err)
	}

	// Update the status
	failover.Status.Status = status
	failover.Status.State = state
	failover.Status.Message = message

	if err := cl.GetClient().Status().Update(ctx, failover); err != nil {
		if !errors.IsConflict(err) {
			return fmt.Errorf("failed to update failover status: %w", err)
		}
		// If there's a conflict, get the latest version and try again
		if err := cl.GetClient().Get(ctx, client.ObjectKey{
			Namespace: failover.Namespace,
			Name:      failover.Name,
		}, failover); err != nil {
			return fmt.Errorf("failed to get latest version after conflict: %w", err)
		}
		// Reapply our changes
		failover.Status.Status = status
		failover.Status.State = state
		failover.Status.Message = message
		if err := cl.GetClient().Status().Update(ctx, failover); err != nil {
			return fmt.Errorf("failed to update failover status after conflict resolution: %w", err)
		}
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *FailoverReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&crdv1alpha1.Failover{}).
		Complete(r)
}

//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers/status,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
//+kubebuilder:rbac:groups=replication.storage.openshift.io,resources=volumereplications,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=replication.storage.openshift.io,resources=volumereplications/status,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=source.toolkit.fluxcd.io,resources=kustomizations,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=helm.toolkit.fluxcd.io,resources=helmreleases,verbs=get;list;watch;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *FailoverReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("failover", req.NamespacedName)
	failover := &crdv1alpha1.Failover{}
	if err := r.Get(ctx, req.NamespacedName, failover); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Get the cluster this failover is in
	clusterName := req.NamespacedName.Namespace
	cl, err := r.Provider.Get(ctx, clusterName)
	if err != nil {
		log.Error(err, "Failed to get cluster", "cluster", clusterName)
		return ctrl.Result{}, err
	}

	// Process the failover
	r.handleFailover(ctx, cl, failover)

	return ctrl.Result{}, nil
}
