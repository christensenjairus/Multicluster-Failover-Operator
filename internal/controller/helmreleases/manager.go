package helmreleases

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Constants for annotations and HelmRelease GVK
const (
	// FluxReconcileAnnotation is the annotation used to trigger manual reconciliation
	FluxReconcileAnnotation = "reconcile.fluxcd.io/requestedAt"

	// DisabledValue is used to disable Flux reconciliation
	DisabledValue = "disabled"
)

// HelmReleaseGVK is the GroupVersionKind for the HelmRelease resource
var HelmReleaseGVK = schema.GroupVersionKind{
	Group:   "helm.toolkit.fluxcd.io",
	Version: "v2beta1",
	Kind:    "HelmRelease",
}

// Manager handles operations related to HelmRelease resources from FluxCD
type Manager struct {
	client client.Client
}

// NewManager creates a new HelmRelease manager
func NewManager(client client.Client) *Manager {
	return &Manager{
		client: client,
	}
}

// TriggerReconciliation triggers a manual reconciliation of a HelmRelease
func (m *Manager) TriggerReconciliation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("helmrelease", name, "namespace", namespace)
	logger.Info("Triggering manual reconciliation of HelmRelease")

	// Get the HelmRelease
	hr, err := m.getHelmRelease(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get HelmRelease")
		return err
	}

	// Add the reconcile annotation with current timestamp
	timestamp := time.Now().Format(time.RFC3339Nano)
	if err := unstructured.SetNestedField(hr.Object, timestamp, "metadata", "annotations", FluxReconcileAnnotation); err != nil {
		logger.Error(err, "Failed to set reconcile annotation")
		return err
	}

	// Update the HelmRelease
	if err := m.client.Update(ctx, hr); err != nil {
		logger.Error(err, "Failed to update HelmRelease")
		return err
	}

	logger.Info("Successfully triggered reconciliation")
	return nil
}

// ForceReconciliation forces a reconciliation regardless of the current state
func (m *Manager) ForceReconciliation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("helmrelease", name, "namespace", namespace)
	logger.Info("Forcing reconciliation of HelmRelease")

	// Get the HelmRelease
	hr, err := m.getHelmRelease(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get HelmRelease")
		return err
	}

	// Remove status to force complete reconciliation
	hr.Object["status"] = nil

	// Add the reconcile annotation with current timestamp
	timestamp := time.Now().Format(time.RFC3339Nano)
	if err := unstructured.SetNestedField(hr.Object, timestamp, "metadata", "annotations", FluxReconcileAnnotation); err != nil {
		logger.Error(err, "Failed to set reconcile annotation")
		return err
	}

	// Update the HelmRelease
	if err := m.client.Update(ctx, hr); err != nil {
		logger.Error(err, "Failed to update HelmRelease")
		return err
	}

	logger.Info("Successfully forced reconciliation")
	return nil
}

// Suspend suspends automatic reconciliation of a HelmRelease
func (m *Manager) Suspend(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("helmrelease", name, "namespace", namespace)
	logger.Info("Suspending HelmRelease reconciliation")

	// Get the HelmRelease
	hr, err := m.getHelmRelease(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get HelmRelease")
		return err
	}

	// Set suspend: true
	if err := unstructured.SetNestedField(hr.Object, true, "spec", "suspend"); err != nil {
		logger.Error(err, "Failed to set suspend field")
		return err
	}

	// Update the HelmRelease
	if err := m.client.Update(ctx, hr); err != nil {
		logger.Error(err, "Failed to update HelmRelease")
		return err
	}

	logger.Info("Successfully suspended HelmRelease")
	return nil
}

// Resume resumes automatic reconciliation of a HelmRelease
func (m *Manager) Resume(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("helmrelease", name, "namespace", namespace)
	logger.Info("Resuming HelmRelease reconciliation")

	// Get the HelmRelease
	hr, err := m.getHelmRelease(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get HelmRelease")
		return err
	}

	// Set suspend: false
	if err := unstructured.SetNestedField(hr.Object, false, "spec", "suspend"); err != nil {
		logger.Error(err, "Failed to set suspend field")
		return err
	}

	// Update the HelmRelease
	if err := m.client.Update(ctx, hr); err != nil {
		logger.Error(err, "Failed to update HelmRelease")
		return err
	}

	logger.Info("Successfully resumed HelmRelease")
	return nil
}

// IsSuspended checks if a HelmRelease is suspended
func (m *Manager) IsSuspended(ctx context.Context, name, namespace string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("helmrelease", name, "namespace", namespace)
	logger.V(1).Info("Checking if HelmRelease is suspended")

	// Get the HelmRelease
	hr, err := m.getHelmRelease(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get HelmRelease")
		return false, err
	}

	// Check the suspend field
	suspended, found, err := unstructured.NestedBool(hr.Object, "spec", "suspend")
	if err != nil {
		logger.Error(err, "Failed to get suspend field")
		return false, err
	}

	if !found {
		logger.V(1).Info("Suspend field not found, assuming not suspended")
		return false, nil
	}

	logger.V(1).Info("HelmRelease suspend state", "suspended", suspended)
	return suspended, nil
}

// AddFluxAnnotation adds the flux reconcile annotation to disable automatic reconciliation
func (m *Manager) AddFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("helmrelease", name, "namespace", namespace)
	logger.Info("Adding Flux annotation to HelmRelease")

	return m.AddAnnotation(ctx, name, namespace, FluxReconcileAnnotation, DisabledValue)
}

// RemoveFluxAnnotation removes the flux reconcile annotation to enable automatic reconciliation
func (m *Manager) RemoveFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("helmrelease", name, "namespace", namespace)
	logger.Info("Removing Flux annotation from HelmRelease")

	return m.RemoveAnnotation(ctx, name, namespace, FluxReconcileAnnotation)
}

// AddAnnotation adds a specific annotation to a HelmRelease
func (m *Manager) AddAnnotation(ctx context.Context, name, namespace, annotationKey, annotationValue string) error {
	logger := log.FromContext(ctx).WithValues("helmrelease", name, "namespace", namespace)
	logger.Info("Adding annotation to HelmRelease", "key", annotationKey, "value", annotationValue)

	// Get the HelmRelease
	hr, err := m.getHelmRelease(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get HelmRelease")
		return err
	}

	// Get existing annotations or create if not exist
	annotations, _, err := unstructured.NestedStringMap(hr.Object, "metadata", "annotations")
	if err != nil {
		logger.Error(err, "Failed to get annotations")
		return err
	}

	if annotations == nil {
		annotations = make(map[string]string)
	}

	// Add the annotation
	annotations[annotationKey] = annotationValue

	// Set the updated annotations
	if err := unstructured.SetNestedStringMap(hr.Object, annotations, "metadata", "annotations"); err != nil {
		logger.Error(err, "Failed to set annotations")
		return err
	}

	// Update the HelmRelease
	if err := m.client.Update(ctx, hr); err != nil {
		logger.Error(err, "Failed to update HelmRelease")
		return err
	}

	logger.Info("Successfully added annotation")
	return nil
}

// RemoveAnnotation removes a specific annotation from a HelmRelease
func (m *Manager) RemoveAnnotation(ctx context.Context, name, namespace, annotationKey string) error {
	logger := log.FromContext(ctx).WithValues("helmrelease", name, "namespace", namespace)
	logger.Info("Removing annotation from HelmRelease", "key", annotationKey)

	// Get the HelmRelease
	hr, err := m.getHelmRelease(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get HelmRelease")
		return err
	}

	// Get existing annotations
	annotations, found, err := unstructured.NestedStringMap(hr.Object, "metadata", "annotations")
	if err != nil {
		logger.Error(err, "Failed to get annotations")
		return err
	}

	if !found || annotations == nil {
		logger.V(1).Info("No annotations found, nothing to remove")
		return nil
	}

	// Check if the annotation exists
	if _, exists := annotations[annotationKey]; !exists {
		logger.V(1).Info("Annotation doesn't exist, nothing to remove")
		return nil
	}

	// Remove the annotation
	delete(annotations, annotationKey)

	// Set the updated annotations
	if err := unstructured.SetNestedStringMap(hr.Object, annotations, "metadata", "annotations"); err != nil {
		logger.Error(err, "Failed to set annotations")
		return err
	}

	// Update the HelmRelease
	if err := m.client.Update(ctx, hr); err != nil {
		logger.Error(err, "Failed to update HelmRelease")
		return err
	}

	logger.Info("Successfully removed annotation")
	return nil
}

// GetAnnotation gets the value of a specific annotation from a HelmRelease
func (m *Manager) GetAnnotation(ctx context.Context, name, namespace, annotationKey string) (string, bool, error) {
	logger := log.FromContext(ctx).WithValues("helmrelease", name, "namespace", namespace)
	logger.V(1).Info("Getting annotation from HelmRelease", "key", annotationKey)

	// Get the HelmRelease
	hr, err := m.getHelmRelease(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get HelmRelease")
		return "", false, err
	}

	// Get annotations
	annotations, found, err := unstructured.NestedStringMap(hr.Object, "metadata", "annotations")
	if err != nil {
		logger.Error(err, "Failed to get annotations")
		return "", false, err
	}

	if !found || annotations == nil {
		logger.V(1).Info("No annotations found")
		return "", false, nil
	}

	// Get the annotation value
	value, exists := annotations[annotationKey]
	if !exists {
		logger.V(1).Info("Annotation not found")
		return "", false, nil
	}

	logger.V(1).Info("Annotation found", "value", value)
	return value, true, nil
}

// WaitForReconciliation waits for a HelmRelease to be reconciled
func (m *Manager) WaitForReconciliation(ctx context.Context, name, namespace string, timeout time.Duration) error {
	logger := log.FromContext(ctx).WithValues("helmrelease", name, "namespace", namespace)
	logger.Info("Waiting for HelmRelease to be reconciled", "timeout", timeout)

	return wait.PollImmediate(2*time.Second, timeout, func() (bool, error) {
		reconciled, err := m.IsReconciled(ctx, name, namespace)
		if err != nil {
			logger.Error(err, "Failed to check if HelmRelease is reconciled")
			return false, nil // Continue polling
		}

		if reconciled {
			logger.Info("HelmRelease successfully reconciled")
			return true, nil
		}

		logger.V(1).Info("HelmRelease not yet reconciled")
		return false, nil
	})
}

// IsReconciled checks if a HelmRelease has been successfully reconciled
func (m *Manager) IsReconciled(ctx context.Context, name, namespace string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("helmrelease", name, "namespace", namespace)
	logger.V(1).Info("Checking if HelmRelease is reconciled")

	// Get the HelmRelease
	hr, err := m.getHelmRelease(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get HelmRelease")
		return false, err
	}

	// Check status conditions
	conditions, found, err := unstructured.NestedSlice(hr.Object, "status", "conditions")
	if err != nil {
		logger.Error(err, "Failed to get conditions")
		return false, err
	}

	if !found || len(conditions) == 0 {
		logger.V(1).Info("No conditions found, assuming not reconciled")
		return false, nil
	}

	// Look for Ready condition with status True
	for _, c := range conditions {
		condition, ok := c.(map[string]interface{})
		if !ok {
			continue
		}

		conditionType, typeFound, _ := unstructured.NestedString(condition, "type")
		status, statusFound, _ := unstructured.NestedString(condition, "status")

		if typeFound && statusFound && conditionType == "Ready" && status == "True" {
			logger.V(1).Info("HelmRelease is reconciled")
			return true, nil
		}
	}

	logger.V(1).Info("HelmRelease is not reconciled")
	return false, nil
}

// Helper function to get a HelmRelease by name and namespace
func (m *Manager) getHelmRelease(ctx context.Context, name, namespace string) (*unstructured.Unstructured, error) {
	hr := &unstructured.Unstructured{}
	hr.SetGroupVersionKind(HelmReleaseGVK)

	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, hr)
	return hr, err
}
