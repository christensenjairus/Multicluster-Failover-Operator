package kustomizations

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

// Constants for annotations and Kustomization GVK
const (
	// FluxReconcileAnnotation is the annotation used to trigger manual reconciliation
	FluxReconcileAnnotation = "reconcile.fluxcd.io/requestedAt"

	// DisabledValue is used to disable Flux reconciliation
	DisabledValue = "disabled"
)

// KustomizationGVK is the GroupVersionKind for the Kustomization resource
var KustomizationGVK = schema.GroupVersionKind{
	Group:   "kustomize.toolkit.fluxcd.io",
	Version: "v1beta2",
	Kind:    "Kustomization",
}

// Manager handles operations related to Kustomization resources from FluxCD
type Manager struct {
	client client.Client
}

// NewManager creates a new Kustomization manager
func NewManager(client client.Client) *Manager {
	return &Manager{
		client: client,
	}
}

// TriggerReconciliation triggers a manual reconciliation of a Kustomization
func (m *Manager) TriggerReconciliation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("kustomization", name, "namespace", namespace)
	logger.Info("Triggering manual reconciliation of Kustomization")

	// Get the Kustomization
	kust, err := m.getKustomization(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get Kustomization")
		return err
	}

	// Add the reconcile annotation with current timestamp
	timestamp := time.Now().Format(time.RFC3339Nano)
	if err := unstructured.SetNestedField(kust.Object, timestamp, "metadata", "annotations", FluxReconcileAnnotation); err != nil {
		logger.Error(err, "Failed to set reconcile annotation")
		return err
	}

	// Update the Kustomization
	if err := m.client.Update(ctx, kust); err != nil {
		logger.Error(err, "Failed to update Kustomization")
		return err
	}

	logger.Info("Successfully triggered reconciliation")
	return nil
}

// ForceReconciliation forces a reconciliation regardless of the current state
func (m *Manager) ForceReconciliation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("kustomization", name, "namespace", namespace)
	logger.Info("Forcing reconciliation of Kustomization")

	// Get the Kustomization
	kust, err := m.getKustomization(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get Kustomization")
		return err
	}

	// Remove status to force complete reconciliation
	kust.Object["status"] = nil

	// Add the reconcile annotation with current timestamp
	timestamp := time.Now().Format(time.RFC3339Nano)
	if err := unstructured.SetNestedField(kust.Object, timestamp, "metadata", "annotations", FluxReconcileAnnotation); err != nil {
		logger.Error(err, "Failed to set reconcile annotation")
		return err
	}

	// Update the Kustomization
	if err := m.client.Update(ctx, kust); err != nil {
		logger.Error(err, "Failed to update Kustomization")
		return err
	}

	logger.Info("Successfully forced reconciliation")
	return nil
}

// Suspend suspends automatic reconciliation of a Kustomization
func (m *Manager) Suspend(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("kustomization", name, "namespace", namespace)
	logger.Info("Suspending Kustomization reconciliation")

	// Get the Kustomization
	kust, err := m.getKustomization(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get Kustomization")
		return err
	}

	// Set suspend: true
	if err := unstructured.SetNestedField(kust.Object, true, "spec", "suspend"); err != nil {
		logger.Error(err, "Failed to set suspend field")
		return err
	}

	// Update the Kustomization
	if err := m.client.Update(ctx, kust); err != nil {
		logger.Error(err, "Failed to update Kustomization")
		return err
	}

	logger.Info("Successfully suspended Kustomization")
	return nil
}

// Resume resumes automatic reconciliation of a Kustomization
func (m *Manager) Resume(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("kustomization", name, "namespace", namespace)
	logger.Info("Resuming Kustomization reconciliation")

	// Get the Kustomization
	kust, err := m.getKustomization(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get Kustomization")
		return err
	}

	// Set suspend: false
	if err := unstructured.SetNestedField(kust.Object, false, "spec", "suspend"); err != nil {
		logger.Error(err, "Failed to set suspend field")
		return err
	}

	// Update the Kustomization
	if err := m.client.Update(ctx, kust); err != nil {
		logger.Error(err, "Failed to update Kustomization")
		return err
	}

	logger.Info("Successfully resumed Kustomization")
	return nil
}

// IsSuspended checks if a Kustomization is suspended
func (m *Manager) IsSuspended(ctx context.Context, name, namespace string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("kustomization", name, "namespace", namespace)
	logger.V(1).Info("Checking if Kustomization is suspended")

	// Get the Kustomization
	kust, err := m.getKustomization(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get Kustomization")
		return false, err
	}

	// Check the suspend field
	suspended, found, err := unstructured.NestedBool(kust.Object, "spec", "suspend")
	if err != nil {
		logger.Error(err, "Failed to get suspend field")
		return false, err
	}

	if !found {
		logger.V(1).Info("Suspend field not found, assuming not suspended")
		return false, nil
	}

	logger.V(1).Info("Kustomization suspend state", "suspended", suspended)
	return suspended, nil
}

// AddFluxAnnotation adds the flux reconcile annotation to disable automatic reconciliation
func (m *Manager) AddFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("kustomization", name, "namespace", namespace)
	logger.Info("Adding Flux annotation to Kustomization")

	return m.AddAnnotation(ctx, name, namespace, FluxReconcileAnnotation, DisabledValue)
}

// RemoveFluxAnnotation removes the flux reconcile annotation to enable automatic reconciliation
func (m *Manager) RemoveFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("kustomization", name, "namespace", namespace)
	logger.Info("Removing Flux annotation from Kustomization")

	return m.RemoveAnnotation(ctx, name, namespace, FluxReconcileAnnotation)
}

// AddAnnotation adds a specific annotation to a Kustomization
func (m *Manager) AddAnnotation(ctx context.Context, name, namespace, annotationKey, annotationValue string) error {
	logger := log.FromContext(ctx).WithValues("kustomization", name, "namespace", namespace)
	logger.Info("Adding annotation to Kustomization", "key", annotationKey, "value", annotationValue)

	// Get the Kustomization
	kust, err := m.getKustomization(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get Kustomization")
		return err
	}

	// Get existing annotations or create if not exist
	annotations, _, err := unstructured.NestedStringMap(kust.Object, "metadata", "annotations")
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
	if err := unstructured.SetNestedStringMap(kust.Object, annotations, "metadata", "annotations"); err != nil {
		logger.Error(err, "Failed to set annotations")
		return err
	}

	// Update the Kustomization
	if err := m.client.Update(ctx, kust); err != nil {
		logger.Error(err, "Failed to update Kustomization")
		return err
	}

	logger.Info("Successfully added annotation")
	return nil
}

// RemoveAnnotation removes a specific annotation from a Kustomization
func (m *Manager) RemoveAnnotation(ctx context.Context, name, namespace, annotationKey string) error {
	logger := log.FromContext(ctx).WithValues("kustomization", name, "namespace", namespace)
	logger.Info("Removing annotation from Kustomization", "key", annotationKey)

	// Get the Kustomization
	kust, err := m.getKustomization(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get Kustomization")
		return err
	}

	// Get existing annotations
	annotations, found, err := unstructured.NestedStringMap(kust.Object, "metadata", "annotations")
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
	if err := unstructured.SetNestedStringMap(kust.Object, annotations, "metadata", "annotations"); err != nil {
		logger.Error(err, "Failed to set annotations")
		return err
	}

	// Update the Kustomization
	if err := m.client.Update(ctx, kust); err != nil {
		logger.Error(err, "Failed to update Kustomization")
		return err
	}

	logger.Info("Successfully removed annotation")
	return nil
}

// GetAnnotation gets the value of a specific annotation from a Kustomization
func (m *Manager) GetAnnotation(ctx context.Context, name, namespace, annotationKey string) (string, bool, error) {
	logger := log.FromContext(ctx).WithValues("kustomization", name, "namespace", namespace)
	logger.V(1).Info("Getting annotation from Kustomization", "key", annotationKey)

	// Get the Kustomization
	kust, err := m.getKustomization(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get Kustomization")
		return "", false, err
	}

	// Get annotations
	annotations, found, err := unstructured.NestedStringMap(kust.Object, "metadata", "annotations")
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

// WaitForReconciliation waits for a Kustomization to be reconciled
func (m *Manager) WaitForReconciliation(ctx context.Context, name, namespace string, timeout time.Duration) error {
	logger := log.FromContext(ctx).WithValues("kustomization", name, "namespace", namespace)
	logger.Info("Waiting for Kustomization to be reconciled", "timeout", timeout)

	return wait.PollImmediate(2*time.Second, timeout, func() (bool, error) {
		reconciled, err := m.IsReconciled(ctx, name, namespace)
		if err != nil {
			logger.Error(err, "Failed to check if Kustomization is reconciled")
			return false, nil // Continue polling
		}

		if reconciled {
			logger.Info("Kustomization successfully reconciled")
			return true, nil
		}

		logger.V(1).Info("Kustomization not yet reconciled")
		return false, nil
	})
}

// IsReconciled checks if a Kustomization has been successfully reconciled
func (m *Manager) IsReconciled(ctx context.Context, name, namespace string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("kustomization", name, "namespace", namespace)
	logger.V(1).Info("Checking if Kustomization is reconciled")

	// Get the Kustomization
	kust, err := m.getKustomization(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get Kustomization")
		return false, err
	}

	// Check status conditions
	conditions, found, err := unstructured.NestedSlice(kust.Object, "status", "conditions")
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
			logger.V(1).Info("Kustomization is reconciled")
			return true, nil
		}
	}

	logger.V(1).Info("Kustomization is not reconciled")
	return false, nil
}

// Helper function to get a Kustomization by name and namespace
func (m *Manager) getKustomization(ctx context.Context, name, namespace string) (*unstructured.Unstructured, error) {
	kust := &unstructured.Unstructured{}
	kust.SetGroupVersionKind(KustomizationGVK)

	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, kust)
	return kust, err
}
