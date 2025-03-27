package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"

	crdv1alpha1 "github.com/christensenjairus/Multicluster-Failover-Operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
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
func NewFailoverReconciler(mgr mcmanager.Manager, provider *kubeconfigprovider.Provider) *FailoverReconciler {
	return &FailoverReconciler{
		Manager:  mgr,
		Provider: provider,
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

	// Initialize status if needed
	if failover.Status.Status == "" {
		failover.Status.Status = "IN_PROGRESS"
		failover.Status.State = "INITIALIZING"
		if err := cl.GetClient().Status().Update(ctx, failover); err != nil {
			log.Error(err, "Failed to update initial status")
			return
		}
	}

	// Get target cluster
	targetCluster, err := r.Provider.Get(ctx, failover.Spec.TargetCluster)
	if err != nil {
		log.Error(err, "Failed to get target cluster", "cluster", failover.Spec.TargetCluster)
		failover.Status.Status = "FAILED"
		failover.Status.Message = fmt.Sprintf("Failed to get target cluster: %v", err)
		if err := cl.GetClient().Status().Update(ctx, failover); err != nil {
			log.Error(err, "Failed to update status")
		}
		return
	}

	// Verify we can access the target cluster's API server
	if err := targetCluster.GetClient().Get(ctx, client.ObjectKey{Namespace: "default", Name: "kube-system"}, &corev1.Namespace{}); err != nil {
		log.Error(err, "Failed to access target cluster API server", "cluster", failover.Spec.TargetCluster)
		failover.Status.Status = "FAILED"
		failover.Status.Message = fmt.Sprintf("Failed to access target cluster API server: %v", err)
		if err := cl.GetClient().Status().Update(ctx, failover); err != nil {
			log.Error(err, "Failed to update status")
		}
		return
	}

	// Process each failover group
	for i, group := range failover.Spec.FailoverGroups {
		// Skip if already processed
		if i < len(failover.Status.FailoverGroups) {
			continue
		}

		// Initialize group status
		group.Status = "IN_PROGRESS"
		group.StartTime = time.Now().Format(time.RFC3339)
		failover.Status.FailoverGroups = append(failover.Status.FailoverGroups, group)

		// Update status
		if err := cl.GetClient().Status().Update(ctx, failover); err != nil {
			log.Error(err, "Failed to update group status")
			return
		}

		// TODO: Implement actual failover logic for each group
		// This would involve:
		// 1. Scaling down workloads in source cluster
		// 2. Promoting volumes in target cluster (using targetCluster.GetClient())
		// 3. Scaling up workloads in target cluster (using targetCluster.GetClient())
		// 4. Updating network resources
		// 5. Triggering Flux reconciliations if needed

		// For now, just mark as successful
		group.Status = "SUCCESS"
		group.CompletionTime = time.Now().Format(time.RFC3339)
		if err := cl.GetClient().Status().Update(ctx, failover); err != nil {
			log.Error(err, "Failed to update group completion status")
			return
		}
	}

	// Check if all groups are processed
	allComplete := true
	for _, group := range failover.Status.FailoverGroups {
		if group.Status != "SUCCESS" {
			allComplete = false
			break
		}
	}

	if allComplete {
		failover.Status.Status = "SUCCESS"
		failover.Status.Message = "All failover groups processed successfully"
		if err := cl.GetClient().Status().Update(ctx, failover); err != nil {
			log.Error(err, "Failed to update final status")
		}
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *FailoverReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&crdv1alpha1.Failover{}).
		Complete(r)
}

//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers/finalizers,verbs=update

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
