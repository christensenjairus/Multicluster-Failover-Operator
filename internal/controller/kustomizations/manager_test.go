package kustomizations

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

	// Create a test Kustomization
	kust := &unstructured.Unstructured{}
	kust.SetGroupVersionKind(KustomizationGVK)
	kust.SetName("test-kustomization")
	kust.SetNamespace("test-namespace")
	kust.SetAnnotations(map[string]string{
		"test-key": "test-value",
	})
	kust.Object["spec"] = map[string]interface{}{
		"suspend":  false,
		"interval": "5m",
		"path":     "./",
		"sourceRef": map[string]interface{}{
			"kind": "GitRepository",
			"name": "test-repo",
		},
	}
	kust.Object["status"] = map[string]interface{}{
		"conditions": []interface{}{
			map[string]interface{}{
				"type":    "Ready",
				"status":  "True",
				"reason":  "ReconciliationSucceeded",
				"message": "Applied revision: main/1234567890",
			},
		},
	}

	// Add Kustomization to fake client
	client.Create(context.Background(), kust)

	manager := NewManager(client)
	return manager, kust
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
	err := manager.TriggerReconciliation(ctx, "test-kustomization", "test-namespace")

	// Assert
	assert.NoError(t, err)

	// Verify reconciliation was triggered by checking for the annotation
	kust := &unstructured.Unstructured{}
	kust.SetGroupVersionKind(KustomizationGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-kustomization", Namespace: "test-namespace"}, kust)
	assert.NoError(t, err)

	annotations := kust.GetAnnotations()
	assert.Contains(t, annotations, FluxReconcileAnnotation)
	assert.NotEqual(t, DisabledValue, annotations[FluxReconcileAnnotation], "Timestamp should be set, not 'disabled'")
}

func TestForceReconciliation(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()

	// Call the function
	err := manager.ForceReconciliation(ctx, "test-kustomization", "test-namespace")

	// Assert
	assert.NoError(t, err)

	// Verify forced reconciliation - status should be nil and annotation added
	kust := &unstructured.Unstructured{}
	kust.SetGroupVersionKind(KustomizationGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-kustomization", Namespace: "test-namespace"}, kust)
	assert.NoError(t, err)

	// Check for nil status value (even if key exists)
	status, _, _ := unstructured.NestedFieldNoCopy(kust.Object, "status")
	assert.Nil(t, status, "Status should be nil")

	// Reconciliation annotation should exist
	annotations := kust.GetAnnotations()
	assert.Contains(t, annotations, FluxReconcileAnnotation)
	assert.NotEqual(t, DisabledValue, annotations[FluxReconcileAnnotation], "Timestamp should be set, not 'disabled'")
}

func TestSuspend(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()

	// Call the function
	err := manager.Suspend(ctx, "test-kustomization", "test-namespace")

	// Assert
	assert.NoError(t, err)

	// Verify the Kustomization was suspended
	kust := &unstructured.Unstructured{}
	kust.SetGroupVersionKind(KustomizationGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-kustomization", Namespace: "test-namespace"}, kust)
	assert.NoError(t, err)

	suspended, found, err := unstructured.NestedBool(kust.Object, "spec", "suspend")
	assert.NoError(t, err)
	assert.True(t, found, "Suspend field should exist")
	assert.True(t, suspended, "Kustomization should be suspended")
}

func TestResume(t *testing.T) {
	// Setup
	manager, kust := setupTestManager()
	ctx := context.Background()

	// First suspend the Kustomization
	unstructured.SetNestedField(kust.Object, true, "spec", "suspend")
	err := manager.client.Update(ctx, kust)
	assert.NoError(t, err)

	// Call the function to resume
	err = manager.Resume(ctx, "test-kustomization", "test-namespace")

	// Assert
	assert.NoError(t, err)

	// Verify the Kustomization was resumed
	updatedKust := &unstructured.Unstructured{}
	updatedKust.SetGroupVersionKind(KustomizationGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-kustomization", Namespace: "test-namespace"}, updatedKust)
	assert.NoError(t, err)

	suspended, found, err := unstructured.NestedBool(updatedKust.Object, "spec", "suspend")
	assert.NoError(t, err)
	assert.True(t, found, "Suspend field should exist")
	assert.False(t, suspended, "Kustomization should not be suspended")
}

func TestIsSuspended(t *testing.T) {
	// Setup
	manager, kust := setupTestManager()
	ctx := context.Background()

	// Test when not suspended
	suspended, err := manager.IsSuspended(ctx, "test-kustomization", "test-namespace")
	assert.NoError(t, err)
	assert.False(t, suspended, "Kustomization should not be suspended initially")

	// Change to suspended state
	unstructured.SetNestedField(kust.Object, true, "spec", "suspend")
	err = manager.client.Update(ctx, kust)
	assert.NoError(t, err)

	// Test when suspended
	suspended, err = manager.IsSuspended(ctx, "test-kustomization", "test-namespace")
	assert.NoError(t, err)
	assert.True(t, suspended, "Kustomization should be suspended")
}

func TestAddFluxAnnotation(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()

	// Call the function
	err := manager.AddFluxAnnotation(ctx, "test-kustomization", "test-namespace")

	// Assert
	assert.NoError(t, err)

	// Verify the annotation was added
	kust := &unstructured.Unstructured{}
	kust.SetGroupVersionKind(KustomizationGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-kustomization", Namespace: "test-namespace"}, kust)
	assert.NoError(t, err)

	annotations := kust.GetAnnotations()
	assert.Contains(t, annotations, FluxReconcileAnnotation)
	assert.Equal(t, DisabledValue, annotations[FluxReconcileAnnotation])
}

func TestRemoveFluxAnnotation(t *testing.T) {
	// Setup
	manager, kust := setupTestManager()
	ctx := context.Background()

	// First add the Flux annotation
	annotations := kust.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[FluxReconcileAnnotation] = DisabledValue
	kust.SetAnnotations(annotations)
	err := manager.client.Update(ctx, kust)
	assert.NoError(t, err)

	// Call the function
	err = manager.RemoveFluxAnnotation(ctx, "test-kustomization", "test-namespace")

	// Assert
	assert.NoError(t, err)

	// Verify the annotation was removed
	updatedKust := &unstructured.Unstructured{}
	updatedKust.SetGroupVersionKind(KustomizationGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-kustomization", Namespace: "test-namespace"}, updatedKust)
	assert.NoError(t, err)

	updatedAnnotations := updatedKust.GetAnnotations()
	assert.NotContains(t, updatedAnnotations, FluxReconcileAnnotation)
}

func TestAddAnnotation(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()

	// Call the function
	err := manager.AddAnnotation(ctx, "test-kustomization", "test-namespace", "new-test-key", "new-test-value")

	// Assert
	assert.NoError(t, err)

	// Verify the annotation was added
	kust := &unstructured.Unstructured{}
	kust.SetGroupVersionKind(KustomizationGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-kustomization", Namespace: "test-namespace"}, kust)
	assert.NoError(t, err)

	annotations := kust.GetAnnotations()
	assert.Contains(t, annotations, "new-test-key")
	assert.Equal(t, "new-test-value", annotations["new-test-key"])
}

func TestRemoveAnnotation(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()

	// Call the function
	err := manager.RemoveAnnotation(ctx, "test-kustomization", "test-namespace", "test-key")

	// Assert
	assert.NoError(t, err)

	// Verify the annotation was removed
	kust := &unstructured.Unstructured{}
	kust.SetGroupVersionKind(KustomizationGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: "test-kustomization", Namespace: "test-namespace"}, kust)
	assert.NoError(t, err)

	annotations := kust.GetAnnotations()
	assert.NotContains(t, annotations, "test-key")
}

func TestGetAnnotation(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()

	// Test getting existing annotation
	value, exists, err := manager.GetAnnotation(ctx, "test-kustomization", "test-namespace", "test-key")
	assert.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "test-value", value)

	// Test getting non-existent annotation
	value, exists, err = manager.GetAnnotation(ctx, "test-kustomization", "test-namespace", "non-existent-key")
	assert.NoError(t, err)
	assert.False(t, exists)
	assert.Equal(t, "", value)
}

func TestWaitForReconciliation(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()

	// Since our test Kustomization is already reconciled, this should return immediately
	err := manager.WaitForReconciliation(ctx, "test-kustomization", "test-namespace", 1*time.Second)
	assert.NoError(t, err)

	// Create another test case with a Kustomization that is not reconciled
	notReconciledKust := &unstructured.Unstructured{}
	notReconciledKust.SetGroupVersionKind(KustomizationGVK)
	notReconciledKust.SetName("not-reconciled")
	notReconciledKust.SetNamespace("test-namespace")
	notReconciledKust.Object["spec"] = map[string]interface{}{
		"suspend": false,
	}
	// No Ready condition in status
	notReconciledKust.Object["status"] = map[string]interface{}{
		"conditions": []interface{}{
			map[string]interface{}{
				"type":   "Reconciling",
				"status": "True",
			},
		},
	}

	err = manager.client.Create(ctx, notReconciledKust)
	assert.NoError(t, err)

	// This should time out since the Kustomization is not reconciled
	err = manager.WaitForReconciliation(ctx, "not-reconciled", "test-namespace", 500*time.Millisecond)
	assert.Error(t, err)
}

func TestIsReconciled(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()

	// Test with already reconciled Kustomization
	reconciled, err := manager.IsReconciled(ctx, "test-kustomization", "test-namespace")
	assert.NoError(t, err)
	assert.True(t, reconciled)

	// Create and test with an unreconciled Kustomization
	notReconciledKust := &unstructured.Unstructured{}
	notReconciledKust.SetGroupVersionKind(KustomizationGVK)
	notReconciledKust.SetName("not-reconciled")
	notReconciledKust.SetNamespace("test-namespace")
	notReconciledKust.Object["spec"] = map[string]interface{}{
		"suspend": false,
	}
	// No Ready condition in status
	notReconciledKust.Object["status"] = map[string]interface{}{
		"conditions": []interface{}{
			map[string]interface{}{
				"type":   "Reconciling",
				"status": "True",
			},
		},
	}

	err = manager.client.Create(ctx, notReconciledKust)
	assert.NoError(t, err)

	reconciled, err = manager.IsReconciled(ctx, "not-reconciled", "test-namespace")
	assert.NoError(t, err)
	assert.False(t, reconciled)

	// Test with a Kustomization that has Ready=False
	notReadyKust := &unstructured.Unstructured{}
	notReadyKust.SetGroupVersionKind(KustomizationGVK)
	notReadyKust.SetName("not-ready")
	notReadyKust.SetNamespace("test-namespace")
	notReadyKust.Object["spec"] = map[string]interface{}{
		"suspend": false,
	}
	notReadyKust.Object["status"] = map[string]interface{}{
		"conditions": []interface{}{
			map[string]interface{}{
				"type":   "Ready",
				"status": "False",
				"reason": "ReconciliationFailed",
			},
		},
	}

	err = manager.client.Create(ctx, notReadyKust)
	assert.NoError(t, err)

	reconciled, err = manager.IsReconciled(ctx, "not-ready", "test-namespace")
	assert.NoError(t, err)
	assert.False(t, reconciled)
}
