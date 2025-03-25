package controller

import (
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/christensenjairus/Multicluster-Failover-Operator/internal/multicluster"
)

// SetupControllers sets up all controllers with the manager
func SetupControllers(mgr multicluster.ClusterManager) error {
	// Add your controller setup functions here
	// Example:
	// if err := (&MyController{}).SetupWithManager(mgr); err != nil {
	//     return fmt.Errorf("unable to create my controller: %w", err)
	// }

	// Example of how to set up a controller that needs multicluster support:
	// if err := (&MyMulticlusterController{
	//     Client:       mgr.GetClient(),
	//     Scheme:       mgr.GetScheme(),
	//     MCReconciler: mgr,
	// }).SetupWithManager(mgr); err != nil {
	//     return fmt.Errorf("unable to create my multicluster controller: %w", err)
	// }

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
