package volumereplications

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ReplicationState represents the possible states of a VolumeReplication resource
type ReplicationState string

const (
	// Primary indicates a read-write state for the volume
	Primary ReplicationState = "primary"

	// Secondary indicates a read-only state for the volume
	Secondary ReplicationState = "secondary"
)

// VolumeReplication GroupVersionKind for unstructured access
var volumeReplicationGVK = schema.GroupVersionKind{
	Group:   "replication.storage.openshift.io",
	Version: "v1alpha1",
	Kind:    "VolumeReplication",
}

// Manager handles operations related to VolumeReplication resources
// This manager provides methods to control the direction of replication during failover
type Manager struct {
	// Kubernetes client for API interactions
	client client.Client
}

// NewManager creates a new VolumeReplication manager
// The client is used to interact with the Kubernetes API server
func NewManager(client client.Client) *Manager {
	return &Manager{
		client: client,
	}
}

// SetVolumeReplicationState changes the replication state of a VolumeReplication resource
// This is used to change the direction of replication during failover operations
// When a cluster becomes PRIMARY, its volumes should be set to Primary (read-write)
// When a cluster becomes STANDBY, its volumes should be set to Secondary (read-only)
func (m *Manager) SetVolumeReplicationState(ctx context.Context, name, namespace string, state ReplicationState) error {
	logger := log.FromContext(ctx).WithValues("volumeReplication", name, "namespace", namespace)
	logger.Info("Setting VolumeReplication state", "state", state)

	// Get the VolumeReplication resource
	vr := &unstructured.Unstructured{}
	vr.SetGroupVersionKind(volumeReplicationGVK)
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, vr)
	if err != nil {
		logger.Error(err, "Failed to get VolumeReplication")
		return err
	}

	// Update the replicationState field in the spec
	if err := unstructured.SetNestedField(vr.Object, string(state), "spec", "replicationState"); err != nil {
		logger.Error(err, "Failed to set replicationState field")
		return err
	}

	// Update the VolumeReplication resource
	err = m.client.Update(ctx, vr)
	if err != nil {
		logger.Error(err, "Failed to update VolumeReplication")
		return err
	}

	logger.Info("Successfully set VolumeReplication state", "state", state)
	return nil
}

// SetPrimary sets a VolumeReplication's state to Primary
// This is typically used during failover to enable read-write access in the new PRIMARY cluster
func (m *Manager) SetPrimary(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("volumeReplication", name, "namespace", namespace)
	logger.Info("Setting VolumeReplication to Primary state")
	return m.SetVolumeReplicationState(ctx, name, namespace, Primary)
}

// SetSecondary sets a VolumeReplication's state to Secondary
// This is typically used during failover to enable read-only access in the STANDBY cluster
func (m *Manager) SetSecondary(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("volumeReplication", name, "namespace", namespace)
	logger.Info("Setting VolumeReplication to Secondary state")
	return m.SetVolumeReplicationState(ctx, name, namespace, Secondary)
}

// GetCurrentState retrieves the current replication state of a VolumeReplication
// Returns Primary or Secondary based on the resource's current configuration
func (m *Manager) GetCurrentState(ctx context.Context, name, namespace string) (ReplicationState, error) {
	logger := log.FromContext(ctx).WithValues("volumeReplication", name, "namespace", namespace)
	logger.V(1).Info("Getting current VolumeReplication state")

	// Get the VolumeReplication resource
	vr := &unstructured.Unstructured{}
	vr.SetGroupVersionKind(volumeReplicationGVK)
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, vr)
	if err != nil {
		logger.Error(err, "Failed to get VolumeReplication")
		return "", err
	}

	// Get the replicationState field from the spec
	state, found, err := unstructured.NestedString(vr.Object, "spec", "replicationState")
	if err != nil {
		logger.Error(err, "Failed to get replicationState field")
		return "", err
	}
	if !found {
		logger.Info("replicationState field not found, assuming Primary")
		return Primary, nil
	}

	logger.V(1).Info("Current VolumeReplication state", "state", state)
	return ReplicationState(state), nil
}

// WaitForStateChange waits for a VolumeReplication to reach the desired state
// This is useful during failover to ensure replication is properly reconfigured before proceeding
func (m *Manager) WaitForStateChange(ctx context.Context, name, namespace string, state ReplicationState, timeout time.Duration) error {
	logger := log.FromContext(ctx).WithValues("volumeReplication", name, "namespace", namespace)
	logger.Info("Waiting for VolumeReplication state change", "desiredState", state, "timeout", timeout)

	return wait.PollImmediate(2*time.Second, timeout, func() (bool, error) {
		currentState, err := m.GetCurrentState(ctx, name, namespace)
		if err != nil {
			logger.Error(err, "Failed to get current state")
			return false, nil // Continue polling
		}

		if currentState == state {
			logger.Info("VolumeReplication reached desired state", "state", state)
			return true, nil
		}

		logger.V(1).Info("VolumeReplication not yet in desired state", "currentState", currentState, "desiredState", state)
		return false, nil
	})
}

// WaitForHealthy waits for a VolumeReplication to be healthy
// A healthy VolumeReplication is one where both Healthy and DataProtected conditions are true
func (m *Manager) WaitForHealthy(ctx context.Context, name, namespace string, timeout time.Duration) error {
	logger := log.FromContext(ctx).WithValues("volumeReplication", name, "namespace", namespace)
	logger.Info("Waiting for VolumeReplication to be healthy", "timeout", timeout)

	return wait.PollImmediate(2*time.Second, timeout, func() (bool, error) {
		healthy, err := m.IsHealthy(ctx, name, namespace)
		if err != nil {
			logger.Error(err, "Failed to check if VolumeReplication is healthy")
			return false, nil // Continue polling
		}

		if healthy {
			logger.Info("VolumeReplication is healthy")
			return true, nil
		}

		logger.V(1).Info("VolumeReplication not yet healthy")
		return false, nil
	})
}

// IsHealthy checks if a VolumeReplication is healthy
// Examines the conditions in the status to determine if replication is working properly
func (m *Manager) IsHealthy(ctx context.Context, name, namespace string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("volumeReplication", name, "namespace", namespace)
	logger.V(1).Info("Checking if VolumeReplication is healthy")

	// Get the VolumeReplication resource
	vr := &unstructured.Unstructured{}
	vr.SetGroupVersionKind(volumeReplicationGVK)
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, vr)
	if err != nil {
		logger.Error(err, "Failed to get VolumeReplication")
		return false, err
	}

	// Get the conditions from the status
	conditions, found, err := unstructured.NestedSlice(vr.Object, "status", "conditions")
	if err != nil {
		logger.Error(err, "Failed to get conditions")
		return false, err
	}
	if !found || len(conditions) == 0 {
		logger.Info("No conditions found, assuming not healthy")
		return false, nil
	}

	// Check for the Healthy and DataProtected conditions
	healthyFound := false
	dataProtectedFound := false

	for _, condition := range conditions {
		conditionMap, ok := condition.(map[string]interface{})
		if !ok {
			continue
		}

		conditionType, found, _ := unstructured.NestedString(conditionMap, "type")
		if !found {
			continue
		}

		status, found, _ := unstructured.NestedString(conditionMap, "status")
		if !found {
			continue
		}

		if conditionType == "Healthy" && status == "True" {
			healthyFound = true
		}

		if conditionType == "DataProtected" && status == "True" {
			dataProtectedFound = true
		}
	}

	isHealthy := healthyFound && dataProtectedFound
	logger.V(1).Info("VolumeReplication health check", "healthy", isHealthy, "healthyCondition", healthyFound, "dataProtectedCondition", dataProtectedFound)
	return isHealthy, nil
}

// IsPrimary checks if a VolumeReplication is in Primary state
// Used to determine the current role of a volume in the replication relationship
func (m *Manager) IsPrimary(ctx context.Context, name, namespace string) (bool, error) {
	state, err := m.GetCurrentState(ctx, name, namespace)
	if err != nil {
		return false, err
	}
	return state == Primary, nil
}

// IsSecondary checks if a VolumeReplication is in Secondary state
// Used to determine the current role of a volume in the replication relationship
func (m *Manager) IsSecondary(ctx context.Context, name, namespace string) (bool, error) {
	state, err := m.GetCurrentState(ctx, name, namespace)
	if err != nil {
		return false, err
	}
	return state == Secondary, nil
}

// ProcessVolumeReplications processes a list of VolumeReplications
// Sets all to either Primary or Secondary based on the active parameter
func (m *Manager) ProcessVolumeReplications(ctx context.Context, namespace string, names []string, active bool) {
	logger := log.FromContext(ctx).WithValues("namespace", namespace)
	targetState := Secondary
	if active {
		targetState = Primary
	}
	logger.Info("Processing VolumeReplications", "count", len(names), "targetState", targetState)

	for _, name := range names {
		if err := m.SetVolumeReplicationState(ctx, name, namespace, targetState); err != nil {
			logger.Error(err, "Failed to set VolumeReplication state",
				"volumeReplication", name, "targetState", targetState)
		}
	}
}

// WaitForAllReplicationsHealthy waits for all VolumeReplications to be healthy
// Used during failover to ensure all volumes are properly replicated before proceeding
func (m *Manager) WaitForAllReplicationsHealthy(ctx context.Context, namespace string, names []string, timeout time.Duration) error {
	logger := log.FromContext(ctx).WithValues("namespace", namespace)
	logger.Info("Waiting for all VolumeReplications to be healthy", "count", len(names), "timeout", timeout)

	for _, name := range names {
		if err := m.WaitForHealthy(ctx, name, namespace, timeout); err != nil {
			logger.Error(err, "Timeout waiting for VolumeReplication to be healthy", "volumeReplication", name)
			return fmt.Errorf("timeout waiting for VolumeReplication %s to be healthy: %w", name, err)
		}
	}

	logger.Info("All VolumeReplications are healthy")
	return nil
}

// WaitForAllReplicationsState waits for all VolumeReplications to reach the desired state
// Used during failover to ensure all volumes have the correct replication direction
func (m *Manager) WaitForAllReplicationsState(ctx context.Context, namespace string, names []string, state ReplicationState, timeout time.Duration) error {
	logger := log.FromContext(ctx).WithValues("namespace", namespace)
	logger.Info("Waiting for all VolumeReplications to reach state", "state", state, "count", len(names), "timeout", timeout)

	for _, name := range names {
		if err := m.WaitForStateChange(ctx, name, namespace, state, timeout); err != nil {
			logger.Error(err, "Timeout waiting for VolumeReplication state change",
				"volumeReplication", name, "desiredState", state)
			return fmt.Errorf("timeout waiting for VolumeReplication %s to reach state %s: %w",
				name, state, err)
		}
	}

	logger.Info("All VolumeReplications reached desired state", "state", state)
	return nil
}
