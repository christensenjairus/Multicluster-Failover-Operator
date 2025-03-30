package virtualservices

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// setupTestManager creates a test manager with a fake client and a test VirtualService
func setupTestManager() (*Manager, *unstructured.Unstructured) {
	// Create a scheme
	scheme := runtime.NewScheme()

	// Create a test VirtualService as unstructured
	vs := &unstructured.Unstructured{}
	vs.SetGroupVersionKind(VirtualServiceGVK)
	vs.SetName("test-vs")
	vs.SetNamespace("default")
	vs.SetAnnotations(map[string]string{
		"annotation-key": "annotation-value",
	})

	// Set some basic spec fields
	err := unstructured.SetNestedStringSlice(vs.Object, []string{"example.com"}, "spec", "hosts")
	if err != nil {
		panic(err)
	}

	// Create a fake client with the test VirtualService
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(vs).
		Build()

	return NewManager(client), vs
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

func TestUpdateVirtualService(t *testing.T) {
	// Setup
	manager, vs := setupTestManager()
	ctx := context.Background()

	// Call the function to update the VirtualService to active state
	updated, err := manager.UpdateVirtualService(ctx, vs.GetName(), vs.GetNamespace(), "active")

	// Verify operation succeeded
	assert.NoError(t, err)
	assert.True(t, updated, "VirtualService should be marked as updated")

	// Get the updated VirtualService
	updatedVS := &unstructured.Unstructured{}
	updatedVS.SetGroupVersionKind(VirtualServiceGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: vs.GetName(), Namespace: vs.GetNamespace()}, updatedVS)
	assert.NoError(t, err)

	// Verify annotations were set correctly for active state
	annotations := updatedVS.GetAnnotations()
	assert.Equal(t, DNSControllerEnabled, annotations[DNSControllerAnnotation])
	assert.Equal(t, DisabledValue, annotations[FluxReconcileAnnotation])

	// Test updating to passive state
	updated, err = manager.UpdateVirtualService(ctx, vs.GetName(), vs.GetNamespace(), "passive")
	assert.NoError(t, err)
	assert.True(t, updated, "VirtualService should be marked as updated")

	// Get the updated VirtualService again
	err = manager.client.Get(ctx, types.NamespacedName{Name: vs.GetName(), Namespace: vs.GetNamespace()}, updatedVS)
	assert.NoError(t, err)

	// Verify annotations were set correctly for passive state
	annotations = updatedVS.GetAnnotations()
	assert.Equal(t, DNSControllerDisabled, annotations[DNSControllerAnnotation])
	assert.Equal(t, DisabledValue, annotations[FluxReconcileAnnotation])
}

func TestProcessVirtualServices(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()
	vsNames := []string{"test-vs"}

	// Setup a second VirtualService for testing multiple VirtualServices
	secondVS := &unstructured.Unstructured{}
	secondVS.SetGroupVersionKind(VirtualServiceGVK)
	secondVS.SetName("test-vs2")
	secondVS.SetNamespace("default")
	secondVS.SetAnnotations(map[string]string{})
	err := unstructured.SetNestedStringSlice(secondVS.Object, []string{"example2.com"}, "spec", "hosts")
	assert.NoError(t, err)
	err = manager.client.Create(ctx, secondVS)
	assert.NoError(t, err)
	vsNames = append(vsNames, "test-vs2")

	// Test with active state
	manager.ProcessVirtualServices(ctx, "default", vsNames, "active")

	// Verify all VirtualServices have been updated
	for _, name := range vsNames {
		vs := &unstructured.Unstructured{}
		vs.SetGroupVersionKind(VirtualServiceGVK)
		err := manager.client.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, vs)
		assert.NoError(t, err)
		annotations := vs.GetAnnotations()
		assert.Equal(t, DNSControllerEnabled, annotations[DNSControllerAnnotation])
		assert.Equal(t, DisabledValue, annotations[FluxReconcileAnnotation])
	}

	// Test with passive state
	manager.ProcessVirtualServices(ctx, "default", vsNames, "passive")

	// Verify all VirtualServices have been updated
	for _, name := range vsNames {
		vs := &unstructured.Unstructured{}
		vs.SetGroupVersionKind(VirtualServiceGVK)
		err := manager.client.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, vs)
		assert.NoError(t, err)
		annotations := vs.GetAnnotations()
		assert.Equal(t, DNSControllerDisabled, annotations[DNSControllerAnnotation])
		assert.Equal(t, DisabledValue, annotations[FluxReconcileAnnotation])
	}
}

func TestAddFluxAnnotation(t *testing.T) {
	// Setup
	manager, vs := setupTestManager()
	ctx := context.Background()

	// Call the function to add flux annotation
	err := manager.AddFluxAnnotation(ctx, vs.GetName(), vs.GetNamespace())

	// Verify operation succeeded
	assert.NoError(t, err)

	// Get the updated VirtualService
	updatedVS := &unstructured.Unstructured{}
	updatedVS.SetGroupVersionKind(VirtualServiceGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: vs.GetName(), Namespace: vs.GetNamespace()}, updatedVS)
	assert.NoError(t, err)

	// Verify annotation was added
	annotations := updatedVS.GetAnnotations()
	assert.Equal(t, DisabledValue, annotations[FluxReconcileAnnotation])
}

func TestRemoveFluxAnnotation(t *testing.T) {
	// Setup
	manager, vs := setupTestManager()
	ctx := context.Background()

	// First add the flux annotation to the test VirtualService
	annotations := vs.GetAnnotations()
	annotations[FluxReconcileAnnotation] = DisabledValue
	vs.SetAnnotations(annotations)
	err := manager.client.Update(ctx, vs)
	assert.NoError(t, err)

	// Call the function to remove flux annotation
	err = manager.RemoveFluxAnnotation(ctx, vs.GetName(), vs.GetNamespace())

	// Verify operation succeeded
	assert.NoError(t, err)

	// Get the updated VirtualService
	updatedVS := &unstructured.Unstructured{}
	updatedVS.SetGroupVersionKind(VirtualServiceGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: vs.GetName(), Namespace: vs.GetNamespace()}, updatedVS)
	assert.NoError(t, err)

	// Verify annotation was removed
	annotations = updatedVS.GetAnnotations()
	_, exists := annotations[FluxReconcileAnnotation]
	assert.False(t, exists)
}

func TestAddAnnotation(t *testing.T) {
	// Setup
	manager, vs := setupTestManager()
	ctx := context.Background()

	// Call the function to add annotation
	err := manager.AddAnnotation(ctx, vs.GetName(), vs.GetNamespace(), "test-key", "test-value")

	// Verify operation succeeded
	assert.NoError(t, err)

	// Get the updated VirtualService
	updatedVS := &unstructured.Unstructured{}
	updatedVS.SetGroupVersionKind(VirtualServiceGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: vs.GetName(), Namespace: vs.GetNamespace()}, updatedVS)
	assert.NoError(t, err)

	// Verify annotation was added
	annotations := updatedVS.GetAnnotations()
	assert.Equal(t, "test-value", annotations["test-key"])
}

func TestRemoveAnnotation(t *testing.T) {
	// Setup
	manager, vs := setupTestManager()
	ctx := context.Background()

	// Add a test annotation
	annotations := vs.GetAnnotations()
	annotations["test-key"] = "test-value"
	vs.SetAnnotations(annotations)
	err := manager.client.Update(ctx, vs)
	assert.NoError(t, err)

	// Call the function to remove annotation
	err = manager.RemoveAnnotation(ctx, vs.GetName(), vs.GetNamespace(), "test-key")

	// Verify operation succeeded
	assert.NoError(t, err)

	// Get the updated VirtualService
	updatedVS := &unstructured.Unstructured{}
	updatedVS.SetGroupVersionKind(VirtualServiceGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: vs.GetName(), Namespace: vs.GetNamespace()}, updatedVS)
	assert.NoError(t, err)

	// Verify annotation was removed
	annotations = updatedVS.GetAnnotations()
	_, exists := annotations["test-key"]
	assert.False(t, exists)
}

func TestGetAnnotation(t *testing.T) {
	// Setup
	manager, vs := setupTestManager()
	ctx := context.Background()

	// Add a test annotation
	const testKey = "test-key"
	const testValue = "test-value"
	annotations := vs.GetAnnotations()
	annotations[testKey] = testValue
	vs.SetAnnotations(annotations)
	err := manager.client.Update(ctx, vs)
	assert.NoError(t, err)

	// Call the function to get annotation
	value, exists, err := manager.GetAnnotation(ctx, vs.GetName(), vs.GetNamespace(), testKey)

	// Verify operation succeeded and returned the correct values
	assert.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, testValue, value)

	// Test for non-existent annotation
	value, exists, err = manager.GetAnnotation(ctx, vs.GetName(), vs.GetNamespace(), "nonexistent-key")
	assert.NoError(t, err)
	assert.False(t, exists)
	assert.Equal(t, "", value)
}

func TestSetDNSController(t *testing.T) {
	// Setup
	manager, vs := setupTestManager()
	ctx := context.Background()

	// Test enable=true
	err := manager.SetDNSController(ctx, vs.GetName(), vs.GetNamespace(), true)
	assert.NoError(t, err)

	// Get the updated VirtualService
	updatedVS := &unstructured.Unstructured{}
	updatedVS.SetGroupVersionKind(VirtualServiceGVK)
	err = manager.client.Get(ctx, types.NamespacedName{Name: vs.GetName(), Namespace: vs.GetNamespace()}, updatedVS)
	assert.NoError(t, err)

	// Verify annotation was set correctly
	annotations := updatedVS.GetAnnotations()
	assert.Equal(t, DNSControllerEnabled, annotations[DNSControllerAnnotation])

	// Test enable=false
	err = manager.SetDNSController(ctx, vs.GetName(), vs.GetNamespace(), false)
	assert.NoError(t, err)

	// Get the updated VirtualService again
	err = manager.client.Get(ctx, types.NamespacedName{Name: vs.GetName(), Namespace: vs.GetNamespace()}, updatedVS)
	assert.NoError(t, err)

	// Verify annotation was set correctly
	annotations = updatedVS.GetAnnotations()
	assert.Equal(t, DNSControllerDisabled, annotations[DNSControllerAnnotation])
}

func TestIsPrimary(t *testing.T) {
	// Setup
	manager, vs := setupTestManager()
	ctx := context.Background()

	// Test with annotation not set
	isPrimary, err := manager.IsPrimary(ctx, vs.GetName(), vs.GetNamespace())
	assert.NoError(t, err)
	assert.False(t, isPrimary)

	// Set DNS controller annotation to enabled
	annotations := vs.GetAnnotations()
	annotations[DNSControllerAnnotation] = DNSControllerEnabled
	vs.SetAnnotations(annotations)
	err = manager.client.Update(ctx, vs)
	assert.NoError(t, err)

	// Test with annotation set to enabled
	isPrimary, err = manager.IsPrimary(ctx, vs.GetName(), vs.GetNamespace())
	assert.NoError(t, err)
	assert.True(t, isPrimary)

	// Set DNS controller annotation to disabled
	annotations[DNSControllerAnnotation] = DNSControllerDisabled
	vs.SetAnnotations(annotations)
	err = manager.client.Update(ctx, vs)
	assert.NoError(t, err)

	// Test with annotation set to disabled
	isPrimary, err = manager.IsPrimary(ctx, vs.GetName(), vs.GetNamespace())
	assert.NoError(t, err)
	assert.False(t, isPrimary)
}

func TestIsSecondary(t *testing.T) {
	// Setup
	manager, vs := setupTestManager()
	ctx := context.Background()

	// Test with annotation not set
	isSecondary, err := manager.IsSecondary(ctx, vs.GetName(), vs.GetNamespace())
	assert.NoError(t, err)
	assert.True(t, isSecondary, "Should be secondary by default when annotation is not set")

	// Set DNS controller annotation to enabled
	annotations := vs.GetAnnotations()
	annotations[DNSControllerAnnotation] = DNSControllerEnabled
	vs.SetAnnotations(annotations)
	err = manager.client.Update(ctx, vs)
	assert.NoError(t, err)

	// Test with annotation set to enabled
	isSecondary, err = manager.IsSecondary(ctx, vs.GetName(), vs.GetNamespace())
	assert.NoError(t, err)
	assert.False(t, isSecondary, "Should not be secondary when annotation is enabled")

	// Set DNS controller annotation to disabled
	annotations[DNSControllerAnnotation] = DNSControllerDisabled
	vs.SetAnnotations(annotations)
	err = manager.client.Update(ctx, vs)
	assert.NoError(t, err)

	// Test with annotation set to disabled
	isSecondary, err = manager.IsSecondary(ctx, vs.GetName(), vs.GetNamespace())
	assert.NoError(t, err)
	assert.True(t, isSecondary, "Should be secondary when annotation is disabled")
}

func TestIsReady(t *testing.T) {
	// Setup
	manager, vs := setupTestManager()
	ctx := context.Background()

	// Test with existing VirtualService
	isReady, err := manager.IsReady(ctx, vs.GetName(), vs.GetNamespace())
	assert.NoError(t, err)
	assert.True(t, isReady, "Existing VirtualService should be ready")

	// Test with non-existent VirtualService
	isReady, err = manager.IsReady(ctx, "nonexistent-vs", vs.GetNamespace())
	assert.Error(t, err)
	assert.False(t, isReady, "Non-existent VirtualService should not be ready")
}

func TestWaitForReady(t *testing.T) {
	// Setup
	manager, vs := setupTestManager()
	ctx := context.Background()

	// Test with existing VirtualService
	err := manager.WaitForReady(ctx, vs.GetName(), vs.GetNamespace(), 1)
	assert.NoError(t, err)
}

func TestWaitForAllVirtualServicesReady(t *testing.T) {
	// Setup
	manager, vs := setupTestManager()
	ctx := context.Background()

	// Create a second VirtualService
	secondVS := &unstructured.Unstructured{}
	secondVS.SetGroupVersionKind(VirtualServiceGVK)
	secondVS.SetName("test-vs2")
	secondVS.SetNamespace("default")
	secondVS.SetAnnotations(map[string]string{})
	err := unstructured.SetNestedStringSlice(secondVS.Object, []string{"example2.com"}, "spec", "hosts")
	assert.NoError(t, err)
	err = manager.client.Create(ctx, secondVS)
	assert.NoError(t, err)

	// Test with existing VirtualServices
	err = manager.WaitForAllVirtualServicesReady(ctx, "default", []string{vs.GetName(), secondVS.GetName()}, 1)
	assert.NoError(t, err)
}
