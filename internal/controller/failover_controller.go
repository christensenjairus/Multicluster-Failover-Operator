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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	crdv1alpha1 "github.com/christensenjairus/Multicluster-Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Multicluster-Failover-Operator/providers/kubeconfigs"
	corev1 "k8s.io/api/core/v1"
)

// FailoverReconciler reconciles a Failover object
type FailoverReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	MCManager kubeconfigs.Manager
}

// +kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=crd.hahomelabs.com,resources=failovers/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Failover object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *FailoverReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the Failover instance
	failover := &crdv1alpha1.Failover{}
	if err := r.Get(ctx, req.NamespacedName, failover); err != nil {
		if client.IgnoreNotFound(err) != nil {
			logger.Error(err, "unable to fetch Failover")
			return ctrl.Result{}, err
		}
		// Failover was deleted, nothing to do
		return ctrl.Result{}, nil
	}

	// Example: Access a remote cluster using the MCManager
	// This is just a demonstration and should be customized based on your needs
	clusterName := "cluster1" // This would typically come from the Failover spec

	// Get a remote cluster client
	remoteCluster, err := r.MCManager.Get(ctx, clusterName)
	if err != nil {
		logger.Info("Remote cluster not available", "cluster", clusterName, "error", err.Error())
		// Handle the case where the cluster isn't available yet
		// You might want to set a status condition on the Failover object
		return ctrl.Result{RequeueAfter: time.Second * 30}, nil
	}

	// Now you can use the remote cluster client to interact with resources
	// For example, list pods in the default namespace
	pods := &corev1.PodList{}
	if err := remoteCluster.GetClient().List(ctx, pods, client.InNamespace("default")); err != nil {
		logger.Error(err, "failed to list pods in remote cluster", "cluster", clusterName)
		return ctrl.Result{}, err
	}

	logger.Info("Successfully connected to remote cluster",
		"cluster", clusterName,
		"podCount", len(pods.Items))

	// Implement your failover logic here using the remote cluster

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *FailoverReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&crdv1alpha1.Failover{}).
		Complete(r)
}
