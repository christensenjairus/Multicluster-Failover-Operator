package deployments

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// FluxReconcileAnnotation is the annotation used by Flux to control reconciliation
const FluxReconcileAnnotation = "kustomize.toolkit.fluxcd.io/reconcile"

// DisabledValue is the value for the Flux reconcile annotation to disable reconciliation
const DisabledValue = "disabled"

// Manager handles operations related to Deployment resources
// This manager provides methods to control Deployment scale during failover operations
type Manager struct {
	client client.Client
}

// NewManager creates a new Deployment manager
// The client is used to interact with the Kubernetes API
func NewManager(client client.Client) *Manager {
	return &Manager{
		client: client,
	}
}

// ScaleDeployment scales a deployment to the desired number of replicas
// This is the core scaling method for Deployments during failover operations
func (m *Manager) ScaleDeployment(ctx context.Context, name, namespace string, replicas int32) error {
	logger := log.FromContext(ctx).WithValues("deployment", name, "namespace", namespace)
	logger.Info("Scaling deployment", "replicas", replicas)

	// Get the deployment
	deployment := &appsv1.Deployment{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deployment); err != nil {
		logger.Error(err, "Failed to get deployment")
		return err
	}

	// Check if already at desired replica count
	if deployment.Spec.Replicas != nil && *deployment.Spec.Replicas == replicas {
		logger.Info("Deployment already at desired replica count", "replicas", replicas)
		return nil
	}

	// Update the replica count
	deployment.Spec.Replicas = &replicas

	// Update the deployment
	if err := m.client.Update(ctx, deployment); err != nil {
		logger.Error(err, "Failed to update deployment")
		return err
	}

	logger.Info("Successfully scaled deployment", "replicas", replicas)
	return nil
}

// ScaleDown scales a deployment to 0 replicas
// Used during failover to STANDBY state to prevent resource consumption
func (m *Manager) ScaleDown(ctx context.Context, name, namespace string) error {
	return m.ScaleDeployment(ctx, name, namespace, 0)
}

// ScaleUp scales a deployment to the specified number of replicas
// Used during failover to PRIMARY state to restore service availability
func (m *Manager) ScaleUp(ctx context.Context, name, namespace string, replicas int32) error {
	return m.ScaleDeployment(ctx, name, namespace, replicas)
}

// GetCurrentReplicas gets the current replica count for a deployment
// Used to determine the current scale of a deployment before making changes
func (m *Manager) GetCurrentReplicas(ctx context.Context, name, namespace string) (int32, error) {
	logger := log.FromContext(ctx).WithValues("deployment", name, "namespace", namespace)
	logger.V(1).Info("Getting current replicas for deployment")

	// Get the deployment
	deployment := &appsv1.Deployment{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deployment); err != nil {
		logger.Error(err, "Failed to get deployment")
		return 0, err
	}

	// Return 0 if replicas is nil
	if deployment.Spec.Replicas == nil {
		return 0, nil
	}

	return *deployment.Spec.Replicas, nil
}

// WaitForReplicasReady waits until all replicas of a deployment are ready
// This is crucial during failover to ensure the service is fully available before completing
func (m *Manager) WaitForReplicasReady(ctx context.Context, name, namespace string, timeout int) error {
	logger := log.FromContext(ctx).WithValues("deployment", name, "namespace", namespace)
	logger.Info("Waiting for deployment replicas to be ready", "timeout", timeout)

	return wait.PollImmediate(time.Second, time.Duration(timeout)*time.Second, func() (bool, error) {
		ready, err := m.IsReady(ctx, name, namespace)
		if err != nil {
			return false, err
		}
		return ready, nil
	})
}

// IsReady checks if a deployment is ready (all replicas are available)
// Used to determine if a deployment is in the appropriate state for PRIMARY role
func (m *Manager) IsReady(ctx context.Context, name, namespace string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("deployment", name, "namespace", namespace)
	logger.V(1).Info("Checking if deployment is ready")

	// Get the deployment
	deployment := &appsv1.Deployment{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deployment); err != nil {
		logger.Error(err, "Failed to get deployment")
		return false, err
	}

	// Check if desired replicas is 0 (edge case)
	if deployment.Spec.Replicas != nil && *deployment.Spec.Replicas == 0 {
		// If 0 replicas desired, then it's ready when status.replicas is also 0
		return deployment.Status.Replicas == 0, nil
	}

	// For normal deployments, check if all replicas are available
	return deployment.Status.AvailableReplicas == deployment.Status.Replicas &&
		deployment.Status.Replicas == *deployment.Spec.Replicas, nil
}

// IsScaledDown checks if a deployment is scaled down to 0 replicas
// Used to determine if a deployment is in the appropriate state for STANDBY role
func (m *Manager) IsScaledDown(ctx context.Context, name, namespace string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("deployment", name, "namespace", namespace)
	logger.V(1).Info("Checking if deployment is scaled down")

	// Get the deployment
	deployment := &appsv1.Deployment{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deployment); err != nil {
		logger.Error(err, "Failed to get deployment")
		return false, err
	}

	// Check if replicas is nil (defaults to 1) or 0
	return deployment.Spec.Replicas != nil && *deployment.Spec.Replicas == 0, nil
}

// AddFluxAnnotation adds the flux reconcile annotation to disable automatic reconciliation
// This prevents Flux from automatically scaling the deployment back to its GitOps-defined state
func (m *Manager) AddFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("deployment", name, "namespace", namespace)
	logger.Info("Adding Flux annotation to deployment")

	return m.AddAnnotation(ctx, name, namespace, FluxReconcileAnnotation, DisabledValue)
}

// RemoveFluxAnnotation removes the flux reconcile annotation to enable automatic reconciliation
// This allows Flux to resume normal reconciliation after failover is complete
func (m *Manager) RemoveFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("deployment", name, "namespace", namespace)
	logger.Info("Removing Flux annotation from deployment")

	return m.RemoveAnnotation(ctx, name, namespace, FluxReconcileAnnotation)
}

// AddAnnotation adds a specific annotation to a deployment
// This is a general-purpose method for adding any annotation to a deployment
func (m *Manager) AddAnnotation(ctx context.Context, name, namespace, annotationKey, annotationValue string) error {
	logger := log.FromContext(ctx).WithValues("deployment", name, "namespace", namespace)
	logger.Info("Adding annotation to deployment", "key", annotationKey, "value", annotationValue)

	// Get the deployment
	deployment := &appsv1.Deployment{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deployment); err != nil {
		logger.Error(err, "Failed to get deployment")
		return err
	}

	// Initialize annotations map if it doesn't exist
	if deployment.Annotations == nil {
		deployment.Annotations = make(map[string]string)
	}

	// Check if the annotation already exists with the same value
	if value, exists := deployment.Annotations[annotationKey]; exists && value == annotationValue {
		logger.Info("Annotation already exists with the same value", "key", annotationKey, "value", annotationValue)
		return nil
	}

	// Add the annotation
	deployment.Annotations[annotationKey] = annotationValue

	// Update the deployment
	if err := m.client.Update(ctx, deployment); err != nil {
		logger.Error(err, "Failed to update deployment with annotation")
		return err
	}

	logger.Info("Successfully added annotation to deployment", "key", annotationKey, "value", annotationValue)
	return nil
}

// RemoveAnnotation removes a specific annotation from a deployment
// This is a general-purpose method for removing any annotation from a deployment
func (m *Manager) RemoveAnnotation(ctx context.Context, name, namespace, annotationKey string) error {
	logger := log.FromContext(ctx).WithValues("deployment", name, "namespace", namespace)
	logger.Info("Removing annotation from deployment", "key", annotationKey)

	// Get the deployment
	deployment := &appsv1.Deployment{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deployment); err != nil {
		logger.Error(err, "Failed to get deployment")
		return err
	}

	// Check if the annotation exists
	if deployment.Annotations == nil || deployment.Annotations[annotationKey] == "" {
		logger.Info("Annotation doesn't exist, nothing to remove", "key", annotationKey)
		return nil
	}

	// Remove the annotation
	delete(deployment.Annotations, annotationKey)

	// Update the deployment
	if err := m.client.Update(ctx, deployment); err != nil {
		logger.Error(err, "Failed to update deployment after removing annotation")
		return err
	}

	logger.Info("Successfully removed annotation from deployment", "key", annotationKey)
	return nil
}

// GetAnnotation gets the value of a specific annotation from a deployment
// This is a general-purpose method for retrieving any annotation from a deployment
func (m *Manager) GetAnnotation(ctx context.Context, name, namespace, annotationKey string) (string, bool, error) {
	logger := log.FromContext(ctx).WithValues("deployment", name, "namespace", namespace)
	logger.V(1).Info("Getting annotation from deployment", "key", annotationKey)

	// Get the deployment
	deployment := &appsv1.Deployment{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deployment); err != nil {
		logger.Error(err, "Failed to get deployment")
		return "", false, err
	}

	// Check if annotations map exists
	if deployment.Annotations == nil {
		return "", false, nil
	}

	// Get the annotation value
	value, exists := deployment.Annotations[annotationKey]
	return value, exists, nil
}

// ProcessDeployments processes a list of Deployments
// Scales them up or down based on the active parameter
func (m *Manager) ProcessDeployments(ctx context.Context, namespace string, names []string, active bool, targetReplicas int32) {
	logger := log.FromContext(ctx).WithValues("namespace", namespace)
	logger.Info("Processing Deployments", "count", len(names), "active", active, "targetReplicas", targetReplicas)

	for _, name := range names {
		if active {
			if err := m.ScaleUp(ctx, name, namespace, targetReplicas); err != nil {
				logger.Error(err, "Failed to scale up Deployment", "deployment", name)
			}
		} else {
			if err := m.ScaleDown(ctx, name, namespace); err != nil {
				logger.Error(err, "Failed to scale down Deployment", "deployment", name)
			}
		}
	}
}

// WaitForAllDeploymentsState waits for all Deployments to reach a desired state
// If expectScaledDown is true, waits for all to be scaled down; otherwise waits for all to be ready
func (m *Manager) WaitForAllDeploymentsState(ctx context.Context, namespace string, names []string, expectScaledDown bool, timeout time.Duration) error {
	logger := log.FromContext(ctx).WithValues("namespace", namespace)
	state := "ready"
	if expectScaledDown {
		state = "scaled down"
	}
	logger.Info("Waiting for all Deployments to be "+state, "count", len(names), "timeout", timeout)

	errChan := make(chan error, len(names))
	doneChan := make(chan bool, len(names))

	// Process each Deployment concurrently
	for _, name := range names {
		go func(name string) {
			err := wait.PollImmediate(2*time.Second, timeout, func() (bool, error) {
				var isInDesiredState bool
				var err error

				if expectScaledDown {
					isInDesiredState, err = m.IsScaledDown(ctx, name, namespace)
				} else {
					isInDesiredState, err = m.IsReady(ctx, name, namespace)
				}

				if err != nil {
					logger.Error(err, "Failed to check Deployment state", "deployment", name)
					return false, nil // Continue polling
				}

				if isInDesiredState {
					logger.V(1).Info("Deployment reached desired state", "deployment", name, "state", state)
					return true, nil
				}

				logger.V(1).Info("Deployment not yet in desired state", "deployment", name, "state", state)
				return false, nil
			})

			if err != nil {
				errChan <- fmt.Errorf("timeout waiting for Deployment %s to be %s", name, state)
			} else {
				doneChan <- true
			}
		}(name)
	}

	// Wait for all goroutines to complete
	var errs []error
	for i := 0; i < len(names); i++ {
		select {
		case err := <-errChan:
			errs = append(errs, err)
		case <-doneChan:
			// Deployment reached desired state
		}
	}

	if len(errs) > 0 {
		errMsg := fmt.Sprintf("%d Deployments failed to reach %s state:", len(errs), state)
		for _, err := range errs {
			errMsg += "\n- " + err.Error()
		}
		return fmt.Errorf(errMsg)
	}

	logger.Info("All Deployments successfully " + state)
	return nil
}
