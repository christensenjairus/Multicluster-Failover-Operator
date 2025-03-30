package helmreleases

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func setupTestManager() (*Manager, *unstructured.Unstructured) {
	// Create a fake client
	client := fake.NewClientBuilder().Build()

	// Create a test HelmRelease
	hr := &unstructured.Unstructured{}
	hr.SetGroupVersionKind(HelmReleaseGVK)
	hr.SetName("test-helmrelease")
	hr.SetNamespace("test-namespace")
	hr.SetAnnotations(map[string]string{
		"test-key": "test-value",
	})
	hr.Object["spec"] = map[string]interface{}{
		"suspend":  false,
		"interval": "5m",
		"chart": map[string]interface{}{
			"spec": map[string]interface{}{
				"chart": "podinfo",
				"sourceRef": map[string]interface{}{
					"kind": "HelmRepository",
					"name": "podinfo",
				},
			},
		},
	}
	hr.Object["status"] = map[string]interface{}{
		"conditions": []interface{}{
			map[string]interface{}{
				"type":    "Ready",
				"status":  "True",
				"reason":  "ReconciliationSucceeded",
				"message": "Release reconciliation succeeded",
			},
		},
	}

	// Add HelmRelease to fake client
	client.Create(context.Background(), hr)

	manager := NewManager(client)
	return manager, hr
}

func TestNewManager(t *testing.T) {
	// Create a fake client
	client := fake.NewClientBuilder().Build()

	// Create the manager
	manager := NewManager(client)

	// Assert manager is not nil and client is set
	assert.NotNil(t, manager)
	assert.Equal(t, client, manager.client)
}

func TestTriggerReconciliation(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()

	// Call the function
	err := manager.TriggerReconciliation(ctx, "test-helmrelease", "test-namespace")

	// Assert
	assert.NoError(t, err)

	// Verify reconciliation was triggered by checking for the annotation
	hr := &unstructured.Unstructured{}
	hr.SetGroupVersionKind(HelmReleaseGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-helmrelease", Namespace: "test-namespace"}, hr)
	assert.NoError(t, err)

	annotations := hr.GetAnnotations()
	assert.Contains(t, annotations, FluxReconcileAnnotation)
	assert.NotEqual(t, DisabledValue, annotations[FluxReconcileAnnotation], "Timestamp should be set, not 'disabled'")
}

func TestForceReconciliation(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()

	// Call the function
	err := manager.ForceReconciliation(ctx, "test-helmrelease", "test-namespace")

	// Assert
	assert.NoError(t, err)

	// Verify forced reconciliation - status should be nil and annotation added
	hr := &unstructured.Unstructured{}
	hr.SetGroupVersionKind(HelmReleaseGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-helmrelease", Namespace: "test-namespace"}, hr)
	assert.NoError(t, err)

	// Check for nil status value (even if key exists)
	status, _, _ := unstructured.NestedFieldNoCopy(hr.Object, "status")
	assert.Nil(t, status, "Status should be nil")

	// Reconciliation annotation should exist
	annotations := hr.GetAnnotations()
	assert.Contains(t, annotations, FluxReconcileAnnotation)
	assert.NotEqual(t, DisabledValue, annotations[FluxReconcileAnnotation], "Timestamp should be set, not 'disabled'")
}

func TestSuspend(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()

	// Call the function
	err := manager.Suspend(ctx, "test-helmrelease", "test-namespace")

	// Assert
	assert.NoError(t, err)

	// Verify the HelmRelease was suspended
	hr := &unstructured.Unstructured{}
	hr.SetGroupVersionKind(HelmReleaseGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-helmrelease", Namespace: "test-namespace"}, hr)
	assert.NoError(t, err)

	suspended, found, err := unstructured.NestedBool(hr.Object, "spec", "suspend")
	assert.NoError(t, err)
	assert.True(t, found, "Suspend field should exist")
	assert.True(t, suspended, "HelmRelease should be suspended")
}

func TestResume(t *testing.T) {
	// Setup
	manager, hr := setupTestManager()
	ctx := context.Background()

	// First suspend the HelmRelease
	unstructured.SetNestedField(hr.Object, true, "spec", "suspend")
	err := manager.client.Update(ctx, hr)
	assert.NoError(t, err)

	// Call the function to resume
	err = manager.Resume(ctx, "test-helmrelease", "test-namespace")

	// Assert
	assert.NoError(t, err)

	// Verify the HelmRelease was resumed
	updatedHr := &unstructured.Unstructured{}
	updatedHr.SetGroupVersionKind(HelmReleaseGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-helmrelease", Namespace: "test-namespace"}, updatedHr)
	assert.NoError(t, err)

	suspended, found, err := unstructured.NestedBool(updatedHr.Object, "spec", "suspend")
	assert.NoError(t, err)
	assert.True(t, found, "Suspend field should exist")
	assert.False(t, suspended, "HelmRelease should not be suspended")
}

func TestIsSuspended(t *testing.T) {
	// Setup
	manager, hr := setupTestManager()
	ctx := context.Background()

	// Test when not suspended
	suspended, err := manager.IsSuspended(ctx, "test-helmrelease", "test-namespace")
	assert.NoError(t, err)
	assert.False(t, suspended, "HelmRelease should not be suspended initially")

	// Change to suspended state
	unstructured.SetNestedField(hr.Object, true, "spec", "suspend")
	err = manager.client.Update(ctx, hr)
	assert.NoError(t, err)

	// Test when suspended
	suspended, err = manager.IsSuspended(ctx, "test-helmrelease", "test-namespace")
	assert.NoError(t, err)
	assert.True(t, suspended, "HelmRelease should be suspended")
}

func TestAddFluxAnnotation(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()

	// Call the function
	err := manager.AddFluxAnnotation(ctx, "test-helmrelease", "test-namespace")

	// Assert
	assert.NoError(t, err)

	// Verify the annotation was added
	hr := &unstructured.Unstructured{}
	hr.SetGroupVersionKind(HelmReleaseGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-helmrelease", Namespace: "test-namespace"}, hr)
	assert.NoError(t, err)

	annotations := hr.GetAnnotations()
	assert.Contains(t, annotations, FluxReconcileAnnotation)
	assert.Equal(t, DisabledValue, annotations[FluxReconcileAnnotation])
}

func TestRemoveFluxAnnotation(t *testing.T) {
	// Setup
	manager, hr := setupTestManager()
	ctx := context.Background()

	// First add the Flux annotation
	annotations := hr.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[FluxReconcileAnnotation] = DisabledValue
	hr.SetAnnotations(annotations)
	err := manager.client.Update(ctx, hr)
	assert.NoError(t, err)

	// Call the function
	err = manager.RemoveFluxAnnotation(ctx, "test-helmrelease", "test-namespace")

	// Assert
	assert.NoError(t, err)

	// Verify the annotation was removed
	updatedHr := &unstructured.Unstructured{}
	updatedHr.SetGroupVersionKind(HelmReleaseGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-helmrelease", Namespace: "test-namespace"}, updatedHr)
	assert.NoError(t, err)

	updatedAnnotations := updatedHr.GetAnnotations()
	assert.NotContains(t, updatedAnnotations, FluxReconcileAnnotation)
}

func TestAddAnnotation(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()

	// Call the function
	err := manager.AddAnnotation(ctx, "test-helmrelease", "test-namespace", "new-test-key", "new-test-value")

	// Assert
	assert.NoError(t, err)

	// Verify the annotation was added
	hr := &unstructured.Unstructured{}
	hr.SetGroupVersionKind(HelmReleaseGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-helmrelease", Namespace: "test-namespace"}, hr)
	assert.NoError(t, err)

	annotations := hr.GetAnnotations()
	assert.Contains(t, annotations, "new-test-key")
	assert.Equal(t, "new-test-value", annotations["new-test-key"])
}

func TestRemoveAnnotation(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()

	// Call the function
	err := manager.RemoveAnnotation(ctx, "test-helmrelease", "test-namespace", "test-key")

	// Assert
	assert.NoError(t, err)

	// Verify the annotation was removed
	hr := &unstructured.Unstructured{}
	hr.SetGroupVersionKind(HelmReleaseGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-helmrelease", Namespace: "test-namespace"}, hr)
	assert.NoError(t, err)

	annotations := hr.GetAnnotations()
	assert.NotContains(t, annotations, "test-key")
}

func TestGetAnnotation(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()

	// Test getting existing annotation
	value, exists, err := manager.GetAnnotation(ctx, "test-helmrelease", "test-namespace", "test-key")
	assert.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "test-value", value)

	// Test getting non-existent annotation
	value, exists, err = manager.GetAnnotation(ctx, "test-helmrelease", "test-namespace", "non-existent-key")
	assert.NoError(t, err)
	assert.False(t, exists)
	assert.Equal(t, "", value)
}

func TestWaitForReconciliation(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()

	// Since our test HelmRelease is already reconciled, this should return immediately
	err := manager.WaitForReconciliation(ctx, "test-helmrelease", "test-namespace", 1*time.Second)
	assert.NoError(t, err)

	// Create another test case with a HelmRelease that is not reconciled
	notReconciledHr := &unstructured.Unstructured{}
	notReconciledHr.SetGroupVersionKind(HelmReleaseGVK)
	notReconciledHr.SetName("not-reconciled")
	notReconciledHr.SetNamespace("test-namespace")
	notReconciledHr.Object["spec"] = map[string]interface{}{
		"suspend": false,
	}
	// No Ready condition in status
	notReconciledHr.Object["status"] = map[string]interface{}{
		"conditions": []interface{}{
			map[string]interface{}{
				"type":   "Reconciling",
				"status": "True",
			},
		},
	}

	err = manager.client.Create(ctx, notReconciledHr)
	assert.NoError(t, err)

	// This should time out since the HelmRelease is not reconciled
	err = manager.WaitForReconciliation(ctx, "not-reconciled", "test-namespace", 500*time.Millisecond)
	assert.Error(t, err)
}

func TestIsReconciled(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()

	// Test with already reconciled HelmRelease
	reconciled, err := manager.IsReconciled(ctx, "test-helmrelease", "test-namespace")
	assert.NoError(t, err)
	assert.True(t, reconciled)

	// Create and test with an unreconciled HelmRelease
	notReconciledHr := &unstructured.Unstructured{}
	notReconciledHr.SetGroupVersionKind(HelmReleaseGVK)
	notReconciledHr.SetName("not-reconciled")
	notReconciledHr.SetNamespace("test-namespace")
	notReconciledHr.Object["spec"] = map[string]interface{}{
		"suspend": false,
	}
	// No Ready condition in status
	notReconciledHr.Object["status"] = map[string]interface{}{
		"conditions": []interface{}{
			map[string]interface{}{
				"type":   "Reconciling",
				"status": "True",
			},
		},
	}

	err = manager.client.Create(ctx, notReconciledHr)
	assert.NoError(t, err)

	reconciled, err = manager.IsReconciled(ctx, "not-reconciled", "test-namespace")
	assert.NoError(t, err)
	assert.False(t, reconciled)

	// Test with a HelmRelease that has Ready=False
	notReadyHr := &unstructured.Unstructured{}
	notReadyHr.SetGroupVersionKind(HelmReleaseGVK)
	notReadyHr.SetName("not-ready")
	notReadyHr.SetNamespace("test-namespace")
	notReadyHr.Object["spec"] = map[string]interface{}{
		"suspend": false,
	}
	notReadyHr.Object["status"] = map[string]interface{}{
		"conditions": []interface{}{
			map[string]interface{}{
				"type":   "Ready",
				"status": "False",
				"reason": "ReconciliationFailed",
			},
		},
	}

	err = manager.client.Create(ctx, notReadyHr)
	assert.NoError(t, err)

	reconciled, err = manager.IsReconciled(ctx, "not-ready", "test-namespace")
	assert.NoError(t, err)
	assert.False(t, reconciled)
}
