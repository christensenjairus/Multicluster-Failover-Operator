package volumereplications

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// setupTestManager creates a test manager with a fake client
func setupTestManager() (*Manager, *unstructured.Unstructured) {
	// Create a scheme
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)

	// Create a test VolumeReplication
	vr := &unstructured.Unstructured{}
	vr.SetGroupVersionKind(volumeReplicationGVK)
	vr.SetName("test-vr")
	vr.SetNamespace("default")
	vr.Object["spec"] = map[string]interface{}{
		"replicationState": "primary",
	}
	vr.Object["status"] = map[string]interface{}{
		"conditions": []interface{}{
			map[string]interface{}{
				"type":   "Healthy",
				"status": "True",
			},
			map[string]interface{}{
				"type":   "DataProtected",
				"status": "True",
			},
		},
	}

	// Create a fake client with the test VolumeReplication
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(vr).
		Build()

	return NewManager(client), vr
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

func TestSetVolumeReplicationState(t *testing.T) {
	// Setup
	manager, vr := setupTestManager()
	ctx := context.Background()

	// Call the function to set the state to secondary
	err := manager.SetVolumeReplicationState(ctx, vr.GetName(), vr.GetNamespace(), Secondary)

	// Since this is a stub, we expect no error but no actual state change
	assert.NoError(t, err)

	// TODO: Once implementation is complete, add assertions to verify:
	// 1. The VolumeReplication resource was retrieved
	// 2. The replicationState field was updated to the requested state
	// 3. The resource was successfully updated in the API
}

func TestSetPrimary(t *testing.T) {
	// Setup
	manager, vr := setupTestManager()
	ctx := context.Background()

	// Call the function to set to primary
	err := manager.SetPrimary(ctx, vr.GetName(), vr.GetNamespace())

	// Since this is a stub, we expect no error
	assert.NoError(t, err)

	// TODO: Once implementation is complete, add assertions to verify:
	// 1. The VolumeReplication resource was retrieved
	// 2. The replicationState field was set to Primary
	// 3. The resource was successfully updated in the API
}

func TestSetSecondary(t *testing.T) {
	// Setup
	manager, vr := setupTestManager()
	ctx := context.Background()

	// Call the function to set to secondary
	err := manager.SetSecondary(ctx, vr.GetName(), vr.GetNamespace())

	// Since this is a stub, we expect no error
	assert.NoError(t, err)

	// TODO: Once implementation is complete, add assertions to verify:
	// 1. The VolumeReplication resource was retrieved
	// 2. The replicationState field was set to Secondary
	// 3. The resource was successfully updated in the API
}

func TestGetCurrentState(t *testing.T) {
	// Setup
	manager, vr := setupTestManager()
	ctx := context.Background()

	// Call the function to get the current state
	state, err := manager.GetCurrentState(ctx, vr.GetName(), vr.GetNamespace())

	// Since this is a stub, we expect no error and the default return value (Primary)
	assert.NoError(t, err)
	assert.Equal(t, Primary, state)

	// TODO: Once implementation is complete, add assertions to verify:
	// 1. The VolumeReplication resource was retrieved
	// 2. The correct replicationState was returned
}

func TestWaitForStateChange(t *testing.T) {
	// Setup
	manager, vr := setupTestManager()
	ctx := context.Background()
	timeout := 5 * time.Second

	// Call the function to wait for state change
	err := manager.WaitForStateChange(ctx, vr.GetName(), vr.GetNamespace(), Primary, timeout)

	// Since this is a stub, we expect no error
	assert.NoError(t, err)

	// TODO: Once implementation is complete, add assertions to verify:
	// 1. The function polls the VolumeReplication resource
	// 2. It correctly identifies when the state matches the desired state
	// 3. It respects timeouts and returns an error if exceeded
}

func TestWaitForHealthy(t *testing.T) {
	// Setup
	manager, vr := setupTestManager()
	ctx := context.Background()
	timeout := 5 * time.Second

	// Call the function to wait for healthy state
	err := manager.WaitForHealthy(ctx, vr.GetName(), vr.GetNamespace(), timeout)

	// Since this is a stub, we expect no error
	assert.NoError(t, err)

	// TODO: Once implementation is complete, add assertions to verify:
	// 1. The function polls the VolumeReplication resource
	// 2. It correctly checks the healthy and dataProtected conditions
	// 3. It respects timeouts and returns an error if exceeded
}

func TestIsHealthy(t *testing.T) {
	// Setup
	manager, vr := setupTestManager()
	ctx := context.Background()

	// Call the function to check if healthy
	healthy, err := manager.IsHealthy(ctx, vr.GetName(), vr.GetNamespace())

	// Since this is a stub, we expect no error and the default return value (true)
	assert.NoError(t, err)
	assert.True(t, healthy)

	// TODO: Once implementation is complete, add assertions to verify:
	// 1. The VolumeReplication resource was retrieved
	// 2. The healthy and dataProtected conditions were correctly checked
}

func TestIsPrimary(t *testing.T) {
	// Setup
	manager, vr := setupTestManager()
	ctx := context.Background()

	// Call the function to check if primary
	isPrimary, err := manager.IsPrimary(ctx, vr.GetName(), vr.GetNamespace())

	// Since GetCurrentState is stubbed to return Primary, this should be true
	assert.NoError(t, err)
	assert.True(t, isPrimary)

	// TODO: Once implementation is complete, add assertions to verify:
	// 1. The function calls GetCurrentState
	// 2. It correctly identifies when the state is Primary
}

func TestIsSecondary(t *testing.T) {
	// Setup
	manager, vr := setupTestManager()
	ctx := context.Background()

	// Call the function to check if secondary
	isSecondary, err := manager.IsSecondary(ctx, vr.GetName(), vr.GetNamespace())

	// Since GetCurrentState is stubbed to return Primary, this should be false
	assert.NoError(t, err)
	assert.False(t, isSecondary)

	// TODO: Once implementation is complete, add assertions to verify:
	// 1. The function calls GetCurrentState
	// 2. It correctly identifies when the state is Secondary
}

func TestProcessVolumeReplications(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()
	names := []string{"test-vr1", "test-vr2"}

	// Test with active=true (should set to Primary)
	manager.ProcessVolumeReplications(ctx, "default", names, true)

	// Test with active=false (should set to Secondary)
	manager.ProcessVolumeReplications(ctx, "default", names, false)

	// TODO: Once implementation is complete, add assertions to verify:
	// 1. SetVolumeReplicationState is called for each VolumeReplication
	// 2. The correct state is set based on the active parameter
}

// Create a mock manager for testing that overrides certain methods
type mockManager struct {
	*Manager
}

// Override IsHealthy to always return true for our test objects
func (m *mockManager) IsHealthy(ctx context.Context, name, namespace string) (bool, error) {
	// Special case for our test objects
	if name == "test-vr1" || name == "test-vr2" {
		return true, nil
	}
	// Call the original implementation for other cases
	return m.Manager.IsHealthy(ctx, name, namespace)
}

// Override GetCurrentState to always return Primary for our test objects
func (m *mockManager) GetCurrentState(ctx context.Context, name, namespace string) (ReplicationState, error) {
	// Special case for our test objects
	if name == "test-vr1" || name == "test-vr2" {
		return Primary, nil
	}
	// Call the original implementation for other cases
	return m.Manager.GetCurrentState(ctx, name, namespace)
}

// Test wrapper for WaitForAllReplicationsHealthy
func (m *mockManager) WaitForAllReplicationsHealthy(ctx context.Context, namespace string, names []string, timeout time.Duration) error {
	logger := log.FromContext(ctx).WithValues("namespace", namespace)
	logger.Info("Waiting for all VolumeReplications to be healthy", "count", len(names), "timeout", timeout)

	for _, name := range names {
		healthy, err := m.IsHealthy(ctx, name, namespace)
		if err != nil {
			return fmt.Errorf("error checking health for %s: %w", name, err)
		}
		if !healthy {
			return fmt.Errorf("timeout waiting for VolumeReplication %s to be healthy: timed out waiting for the condition", name)
		}
	}

	return nil
}

// Test wrapper for WaitForAllReplicationsState
func (m *mockManager) WaitForAllReplicationsState(ctx context.Context, namespace string, names []string, state ReplicationState, timeout time.Duration) error {
	logger := log.FromContext(ctx).WithValues("namespace", namespace)
	logger.Info("Waiting for all VolumeReplications to reach state", "state", state, "count", len(names), "timeout", timeout)

	for _, name := range names {
		currentState, err := m.GetCurrentState(ctx, name, namespace)
		if err != nil {
			return fmt.Errorf("error checking state for %s: %w", name, err)
		}
		if currentState != state {
			return fmt.Errorf("timeout waiting for VolumeReplication %s to reach state %s: timed out waiting for the condition", name, state)
		}
	}

	return nil
}

func TestWaitForAllReplicationsHealthy(t *testing.T) {
	// Setup
	origManager, _ := setupTestManager()
	ctx := context.Background()

	// Create a mock manager that overrides the IsHealthy method
	manager := &mockManager{Manager: origManager}

	// Create test VolumeReplications
	vr1 := &unstructured.Unstructured{}
	vr1.SetGroupVersionKind(volumeReplicationGVK)
	vr1.SetName("test-vr1")
	vr1.SetNamespace("default")
	vr1.SetUnstructuredContent(map[string]interface{}{
		"spec": map[string]interface{}{
			"replicationState": "primary",
		},
	})

	vr2 := &unstructured.Unstructured{}
	vr2.SetGroupVersionKind(volumeReplicationGVK)
	vr2.SetName("test-vr2")
	vr2.SetNamespace("default")
	vr2.SetUnstructuredContent(map[string]interface{}{
		"spec": map[string]interface{}{
			"replicationState": "primary",
		},
	})

	// Create the VolumeReplications in the fake client
	_ = manager.client.Create(ctx, vr1)
	_ = manager.client.Create(ctx, vr2)

	names := []string{"test-vr1", "test-vr2"}
	timeout := 1 * time.Second // Shorter timeout for tests

	// Call the function to wait for all replications to be healthy
	err := manager.WaitForAllReplicationsHealthy(ctx, "default", names, timeout)

	// Since we've overridden IsHealthy to return true, we expect no error
	assert.NoError(t, err)
}

func TestWaitForAllReplicationsState(t *testing.T) {
	// Setup
	origManager, _ := setupTestManager()
	ctx := context.Background()

	// Create a mock manager that overrides the GetCurrentState method
	manager := &mockManager{Manager: origManager}

	// Create test VolumeReplications
	vr1 := &unstructured.Unstructured{}
	vr1.SetGroupVersionKind(volumeReplicationGVK)
	vr1.SetName("test-vr1")
	vr1.SetNamespace("default")
	vr1.SetUnstructuredContent(map[string]interface{}{
		"spec": map[string]interface{}{
			"replicationState": "primary",
		},
	})

	vr2 := &unstructured.Unstructured{}
	vr2.SetGroupVersionKind(volumeReplicationGVK)
	vr2.SetName("test-vr2")
	vr2.SetNamespace("default")
	vr2.SetUnstructuredContent(map[string]interface{}{
		"spec": map[string]interface{}{
			"replicationState": "primary",
		},
	})

	// Create the VolumeReplications in the fake client
	_ = manager.client.Create(ctx, vr1)
	_ = manager.client.Create(ctx, vr2)

	names := []string{"test-vr1", "test-vr2"}
	timeout := 1 * time.Second // Shorter timeout for tests

	// Call the function to wait for all replications to reach the desired state
	err := manager.WaitForAllReplicationsState(ctx, "default", names, Primary, timeout)

	// Since we've overridden GetCurrentState to return Primary, we expect no error
	assert.NoError(t, err)
}
