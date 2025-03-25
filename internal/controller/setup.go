package controller

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/christensenjairus/Multicluster-Failover-Operator/internal/multicluster"
)

// SetupControllers sets up all controllers with the manager
func SetupControllers(mgr multicluster.ClusterManager) error {
	// Set up the Failover controller
	if err := (&FailoverReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		MCReconciler: mgr,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create failover controller: %w", err)
	}

	// Set up the FailoverGroup controller
	if err := (&FailoverGroupReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		MCReconciler: mgr,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create failovergroup controller: %w", err)
	}

	return nil
}

// ControllerWithMulticluster is a base struct that can be embedded in controllers
// that need multicluster support
type ControllerWithMulticluster struct {
	Client       manager.Manager
	Scheme       *runtime.Scheme
	MCReconciler multicluster.MulticlusterReconcilerInterface
}

// GetClient returns the client
func (c *ControllerWithMulticluster) GetClient() client.Client {
	return c.Client.GetClient()
}

// GetScheme returns the scheme
func (c *ControllerWithMulticluster) GetScheme() *runtime.Scheme {
	return c.Client.GetScheme()
}

// GetMCReconciler returns the multicluster reconciler
func (c *ControllerWithMulticluster) GetMCReconciler() multicluster.MulticlusterReconcilerInterface {
	return c.MCReconciler
}
