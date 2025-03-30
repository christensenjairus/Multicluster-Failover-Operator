package statefulsets

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

const (
	// FluxReconcileAnnotation is the annotation used by Flux to control reconciliation
	FluxReconcileAnnotation = "kustomize.toolkit.fluxcd.io/reconcile"

	// DisabledValue is the value for the Flux reconcile annotation to disable reconciliation
	DisabledValue = "disabled"
)

// Manager handles operations related to StatefulSet resources
// This manager provides methods to scale StatefulSets up and down during failover
type Manager struct {
	// Kubernetes client for API interactions
	client client.Client
}

// NewManager creates a new StatefulSet manager
// The client is used to interact with the Kubernetes API server
func NewManager(client client.Client) *Manager {
	return &Manager{
		client: client,
	}
}

// ScaleStatefulSet scales a StatefulSet to a specific number of replicas
// This is used during failover to scale up or down StatefulSets based on whether
// the cluster is PRIMARY or STANDBY
func (m *Manager) ScaleStatefulSet(ctx context.Context, name, namespace string, replicas int32) error {
	logger := log.FromContext(ctx).WithValues("statefulset", name, "namespace", namespace)
	logger.Info("Scaling StatefulSet", "targetReplicas", replicas)

	// Get the StatefulSet
	sts := &appsv1.StatefulSet{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, sts); err != nil {
		logger.Error(err, "Failed to get StatefulSet")
		return err
	}

	// Set the replicas
	sts.Spec.Replicas = &replicas

	// Update the StatefulSet
	if err := m.client.Update(ctx, sts); err != nil {
		logger.Error(err, "Failed to update StatefulSet replicas")
		return err
	}

	logger.Info("Successfully scaled StatefulSet", "replicas", replicas)
	return nil
}

// ScaleDown scales a StatefulSet down to 0 replicas
// This is typically used in STANDBY clusters to ensure the StatefulSet is not running
func (m *Manager) ScaleDown(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("statefulset", name, "namespace", namespace)
	logger.Info("Scaling down StatefulSet to 0 replicas")

	return m.ScaleStatefulSet(ctx, name, namespace, 0)
}

// ScaleUp scales a StatefulSet up to the specified number of replicas
// This is typically used in PRIMARY clusters to ensure the StatefulSet is running
func (m *Manager) ScaleUp(ctx context.Context, name, namespace string, replicas int32) error {
	logger := log.FromContext(ctx).WithValues("statefulset", name, "namespace", namespace)
	logger.Info("Scaling up StatefulSet", "targetReplicas", replicas)

	return m.ScaleStatefulSet(ctx, name, namespace, replicas)
}

// GetCurrentReplicas gets the current replica count for a StatefulSet
// This is useful to know how many replicas were configured before scaling down
func (m *Manager) GetCurrentReplicas(ctx context.Context, name, namespace string) (int32, error) {
	logger := log.FromContext(ctx).WithValues("statefulset", name, "namespace", namespace)
	logger.V(1).Info("Getting current replica count")

	// Get the StatefulSet
	sts := &appsv1.StatefulSet{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, sts); err != nil {
		logger.Error(err, "Failed to get StatefulSet")
		return 0, err
	}

	replicas := int32(0)
	if sts.Spec.Replicas != nil {
		replicas = *sts.Spec.Replicas
	}

	logger.V(1).Info("Current replica count", "replicas", replicas)
	return replicas, nil
}

// IsReady checks if a StatefulSet is ready
// A StatefulSet is considered ready when all of its desired replicas are ready
func (m *Manager) IsReady(ctx context.Context, name, namespace string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("statefulset", name, "namespace", namespace)
	logger.V(1).Info("Checking if StatefulSet is ready")

	// Get the StatefulSet
	sts := &appsv1.StatefulSet{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, sts); err != nil {
		logger.Error(err, "Failed to get StatefulSet")
		return false, err
	}

	// Check if the StatefulSet has no desired replicas, which would mean it's scaled down and "ready"
	if sts.Spec.Replicas != nil && *sts.Spec.Replicas == 0 {
		logger.V(1).Info("StatefulSet has 0 desired replicas, considered ready")
		return true, nil
	}

	// Check if all replicas are ready
	isReady := sts.Status.ReadyReplicas == sts.Status.Replicas &&
		sts.Status.Replicas == sts.Status.CurrentReplicas

	logger.V(1).Info("StatefulSet readiness check",
		"ready", isReady,
		"desiredReplicas", sts.Spec.Replicas,
		"currentReplicas", sts.Status.CurrentReplicas,
		"readyReplicas", sts.Status.ReadyReplicas)
	return isReady, nil
}

// IsScaledDown checks if a StatefulSet is scaled down to 0 replicas
// This is used to verify that a StatefulSet in a STANDBY cluster is properly scaled down
func (m *Manager) IsScaledDown(ctx context.Context, name, namespace string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("statefulset", name, "namespace", namespace)
	logger.V(1).Info("Checking if StatefulSet is scaled down")

	replicas, err := m.GetCurrentReplicas(ctx, name, namespace)
	if err != nil {
		return false, err
	}

	isScaledDown := replicas == 0
	logger.V(1).Info("StatefulSet scale down check", "scaledDown", isScaledDown, "replicas", replicas)
	return isScaledDown, nil
}

// AddAnnotation adds an annotation to a StatefulSet
// Used for various purposes such as disabling Flux reconciliation or providing metadata
func (m *Manager) AddAnnotation(ctx context.Context, name, namespace, key, value string) error {
	logger := log.FromContext(ctx).WithValues("statefulset", name, "namespace", namespace)
	logger.Info("Adding annotation to StatefulSet", "key", key, "value", value)

	// Get the StatefulSet
	sts := &appsv1.StatefulSet{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, sts); err != nil {
		logger.Error(err, "Failed to get StatefulSet")
		return err
	}

	// Add the annotation
	if sts.Annotations == nil {
		sts.Annotations = make(map[string]string)
	}
	sts.Annotations[key] = value

	// Update the StatefulSet
	if err := m.client.Update(ctx, sts); err != nil {
		logger.Error(err, "Failed to update StatefulSet annotations")
		return err
	}

	logger.Info("Successfully added annotation to StatefulSet", "key", key)
	return nil
}

// RemoveAnnotation removes an annotation from a StatefulSet
// Used to clean up annotations that are no longer needed
func (m *Manager) RemoveAnnotation(ctx context.Context, name, namespace, key string) error {
	logger := log.FromContext(ctx).WithValues("statefulset", name, "namespace", namespace)
	logger.Info("Removing annotation from StatefulSet", "key", key)

	// Get the StatefulSet
	sts := &appsv1.StatefulSet{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, sts); err != nil {
		logger.Error(err, "Failed to get StatefulSet")
		return err
	}

	// Remove the annotation if it exists
	if sts.Annotations != nil {
		if _, exists := sts.Annotations[key]; exists {
			delete(sts.Annotations, key)

			// Update the StatefulSet
			if err := m.client.Update(ctx, sts); err != nil {
				logger.Error(err, "Failed to update StatefulSet annotations")
				return err
			}

			logger.Info("Successfully removed annotation from StatefulSet", "key", key)
		} else {
			logger.Info("Annotation does not exist on StatefulSet", "key", key)
		}
	}

	return nil
}

// GetAnnotation gets an annotation from a StatefulSet
// Returns the value and a boolean indicating if the annotation exists
func (m *Manager) GetAnnotation(ctx context.Context, name, namespace, key string) (string, bool, error) {
	logger := log.FromContext(ctx).WithValues("statefulset", name, "namespace", namespace)
	logger.V(1).Info("Getting annotation from StatefulSet", "key", key)

	// Get the StatefulSet
	sts := &appsv1.StatefulSet{}
	if err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, sts); err != nil {
		logger.Error(err, "Failed to get StatefulSet")
		return "", false, err
	}

	// Get the annotation if it exists
	if sts.Annotations != nil {
		value, exists := sts.Annotations[key]
		logger.V(1).Info("StatefulSet annotation status", "key", key, "exists", exists, "value", value)
		return value, exists, nil
	}

	logger.V(1).Info("StatefulSet has no annotations")
	return "", false, nil
}

// AddFluxAnnotation adds the Flux reconcile annotation to a StatefulSet with a value of "disabled"
// This is used to prevent Flux from reconciling the StatefulSet during failover
func (m *Manager) AddFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("statefulset", name, "namespace", namespace)
	logger.Info("Adding Flux reconcile annotation to StatefulSet")

	return m.AddAnnotation(ctx, name, namespace, FluxReconcileAnnotation, DisabledValue)
}

// RemoveFluxAnnotation removes the Flux reconcile annotation from a StatefulSet
// This allows Flux to resume reconciling the StatefulSet after failover is complete
func (m *Manager) RemoveFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("statefulset", name, "namespace", namespace)
	logger.Info("Removing Flux reconcile annotation from StatefulSet")

	return m.RemoveAnnotation(ctx, name, namespace, FluxReconcileAnnotation)
}

// WaitForReplicasReady waits for a StatefulSet to have all replicas ready
// This is useful during failover to ensure StatefulSets are fully scaled before proceeding
func (m *Manager) WaitForReplicasReady(ctx context.Context, name, namespace string, timeout time.Duration) error {
	logger := log.FromContext(ctx).WithValues("statefulset", name, "namespace", namespace)
	logger.Info("Waiting for StatefulSet replicas to be ready", "timeout", timeout)

	return wait.PollImmediate(2*time.Second, timeout, func() (bool, error) {
		ready, err := m.IsReady(ctx, name, namespace)
		if err != nil {
			logger.Error(err, "Failed to check if StatefulSet is ready")
			return false, nil // Continue polling
		}

		if ready {
			logger.Info("StatefulSet replicas are ready")
			return true, nil
		}

		logger.V(1).Info("StatefulSet replicas not yet ready")
		return false, nil
	})
}

// ProcessStatefulSets processes a list of StatefulSets
// Scales them up or down based on the active parameter
func (m *Manager) ProcessStatefulSets(ctx context.Context, namespace string, names []string, active bool, targetReplicas int32) {
	logger := log.FromContext(ctx).WithValues("namespace", namespace)
	logger.Info("Processing StatefulSets", "count", len(names), "active", active, "targetReplicas", targetReplicas)

	for _, name := range names {
		if active {
			if err := m.ScaleUp(ctx, name, namespace, targetReplicas); err != nil {
				logger.Error(err, "Failed to scale up StatefulSet", "statefulset", name)
			}
		} else {
			if err := m.ScaleDown(ctx, name, namespace); err != nil {
				logger.Error(err, "Failed to scale down StatefulSet", "statefulset", name)
			}
		}
	}
}

// WaitForAllStatefulSetsReady waits for all StatefulSets to be ready
// Used during failover to ensure all StatefulSets are fully scaled before proceeding
func (m *Manager) WaitForAllStatefulSetsReady(ctx context.Context, namespace string, names []string, timeout time.Duration) error {
	logger := log.FromContext(ctx).WithValues("namespace", namespace)
	logger.Info("Waiting for all StatefulSets to be ready", "count", len(names), "timeout", timeout)

	for _, name := range names {
		if err := m.WaitForReplicasReady(ctx, name, namespace, timeout); err != nil {
			logger.Error(err, "Timeout waiting for StatefulSet to be ready", "statefulset", name)
			return fmt.Errorf("timeout waiting for StatefulSet %s to be ready: %w", name, err)
		}
	}

	logger.Info("All StatefulSets are ready")
	return nil
}
