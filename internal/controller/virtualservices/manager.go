package virtualservices

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

// Constants for annotations
const (
	// FluxReconcileAnnotation is the annotation used to disable Flux reconciliation
	FluxReconcileAnnotation = "reconcile.fluxcd.io/requestedAt"

	// DisabledValue is used to disable Flux reconciliation
	DisabledValue = "disabled"

	// DNSControllerAnnotation is the annotation used to control DNS behavior
	DNSControllerAnnotation = "external-dns.alpha.kubernetes.io/controller"

	// DNSControllerEnabled is the value to enable external-dns controller
	DNSControllerEnabled = "dns-controller"

	// DNSControllerDisabled is the value to disable external-dns controller
	DNSControllerDisabled = "ignore"
)

// VirtualServiceGVK represents the GVK for Istio VirtualServices
var VirtualServiceGVK = schema.GroupVersionKind{
	Group:   "networking.istio.io",
	Version: "v1beta1",
	Kind:    "VirtualService",
}

// Manager handles operations related to VirtualService resources
type Manager struct {
	client client.Client
}

// NewManager creates a new VirtualService manager
func NewManager(client client.Client) *Manager {
	return &Manager{
		client: client,
	}
}

// getVirtualService retrieves a VirtualService as an unstructured object
func (m *Manager) getVirtualService(ctx context.Context, name, namespace string) (*unstructured.Unstructured, error) {
	vs := &unstructured.Unstructured{}
	vs.SetGroupVersionKind(VirtualServiceGVK)
	err := m.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, vs)
	return vs, err
}

// UpdateVirtualService updates a VirtualService resource based on the desired state
func (m *Manager) UpdateVirtualService(ctx context.Context, name, namespace, state string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("virtualservice", name, "namespace", namespace)
	logger.Info("Updating VirtualService", "desiredState", state)

	// Get the VirtualService
	vs, err := m.getVirtualService(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get VirtualService")
		return false, err
	}

	// Get current annotations
	annotations := vs.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	// Add Flux annotation to prevent Flux from reconciling during failover
	annotations[FluxReconcileAnnotation] = DisabledValue

	// Set DNS controller annotation based on state
	updated := false
	isActive := state == "active"

	if isActive {
		if annotations[DNSControllerAnnotation] != DNSControllerEnabled {
			annotations[DNSControllerAnnotation] = DNSControllerEnabled
			updated = true
		}
	} else {
		if annotations[DNSControllerAnnotation] != DNSControllerDisabled {
			annotations[DNSControllerAnnotation] = DNSControllerDisabled
			updated = true
		}
	}

	// Update the VirtualService if changes were made
	if updated {
		vs.SetAnnotations(annotations)
		err = m.client.Update(ctx, vs)
		if err != nil {
			logger.Error(err, "Failed to update VirtualService")
			return false, err
		}
		logger.Info("Successfully updated VirtualService", "state", state)
	} else {
		logger.Info("No changes needed for VirtualService", "state", state)
	}

	return updated, nil
}

// ProcessVirtualServices handles the processing of all VirtualServices for a failover operation
func (m *Manager) ProcessVirtualServices(ctx context.Context, namespace string, vsNames []string, desiredState string) {
	logger := log.FromContext(ctx).WithValues("namespace", namespace)
	logger.Info("Processing VirtualServices", "count", len(vsNames), "desiredState", desiredState)

	for _, vsName := range vsNames {
		_, err := m.UpdateVirtualService(ctx, vsName, namespace, desiredState)
		if err != nil {
			logger.Error(err, "Failed to update VirtualService", "virtualservice", vsName)
		}
	}
}

// AddFluxAnnotation adds the flux reconcile annotation to disable automatic reconciliation
func (m *Manager) AddFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("virtualservice", name, "namespace", namespace)
	logger.Info("Adding Flux annotation to VirtualService")
	return m.AddAnnotation(ctx, name, namespace, FluxReconcileAnnotation, DisabledValue)
}

// RemoveFluxAnnotation removes the flux reconcile annotation to enable automatic reconciliation
func (m *Manager) RemoveFluxAnnotation(ctx context.Context, name, namespace string) error {
	logger := log.FromContext(ctx).WithValues("virtualservice", name, "namespace", namespace)
	logger.Info("Removing Flux annotation from VirtualService")
	return m.RemoveAnnotation(ctx, name, namespace, FluxReconcileAnnotation)
}

// AddAnnotation adds a specific annotation to a VirtualService
func (m *Manager) AddAnnotation(ctx context.Context, name, namespace, annotationKey, annotationValue string) error {
	logger := log.FromContext(ctx).WithValues("virtualservice", name, "namespace", namespace)
	logger.Info("Adding annotation to VirtualService", "key", annotationKey, "value", annotationValue)

	// Get the VirtualService
	vs, err := m.getVirtualService(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get VirtualService")
		return err
	}

	// Add the annotation
	annotations := vs.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[annotationKey] = annotationValue
	vs.SetAnnotations(annotations)

	// Update the VirtualService
	err = m.client.Update(ctx, vs)
	if err != nil {
		logger.Error(err, "Failed to update VirtualService with annotation")
		return err
	}

	return nil
}

// RemoveAnnotation removes a specific annotation from a VirtualService
func (m *Manager) RemoveAnnotation(ctx context.Context, name, namespace, annotationKey string) error {
	logger := log.FromContext(ctx).WithValues("virtualservice", name, "namespace", namespace)
	logger.Info("Removing annotation from VirtualService", "key", annotationKey)

	// Get the VirtualService
	vs, err := m.getVirtualService(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get VirtualService")
		return err
	}

	// Remove the annotation if it exists
	annotations := vs.GetAnnotations()
	if annotations != nil {
		if _, exists := annotations[annotationKey]; exists {
			delete(annotations, annotationKey)
			vs.SetAnnotations(annotations)

			// Update the VirtualService
			err = m.client.Update(ctx, vs)
			if err != nil {
				logger.Error(err, "Failed to update VirtualService after removing annotation")
				return err
			}
		}
	}

	return nil
}

// GetAnnotation gets the value of a specific annotation from a VirtualService
func (m *Manager) GetAnnotation(ctx context.Context, name, namespace, annotationKey string) (string, bool, error) {
	logger := log.FromContext(ctx).WithValues("virtualservice", name, "namespace", namespace)
	logger.V(1).Info("Getting annotation from VirtualService", "key", annotationKey)

	// Get the VirtualService
	vs, err := m.getVirtualService(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get VirtualService")
		return "", false, err
	}

	// Get the annotation if it exists
	annotations := vs.GetAnnotations()
	if annotations != nil {
		if value, exists := annotations[annotationKey]; exists {
			return value, true, nil
		}
	}

	return "", false, nil
}

// SetDNSController sets the external-dns controller annotation to enable or disable DNS registration
func (m *Manager) SetDNSController(ctx context.Context, name, namespace string, enable bool) error {
	logger := log.FromContext(ctx).WithValues("virtualservice", name, "namespace", namespace)

	controllerValue := DNSControllerDisabled
	if enable {
		controllerValue = DNSControllerEnabled
	}

	logger.Info("Setting DNS controller annotation", "value", controllerValue)
	return m.AddAnnotation(ctx, name, namespace, DNSControllerAnnotation, controllerValue)
}

// IsPrimary checks if the VirtualService is configured as primary (active)
func (m *Manager) IsPrimary(ctx context.Context, name, namespace string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("virtualservice", name, "namespace", namespace)
	logger.V(1).Info("Checking if VirtualService is primary")

	value, exists, err := m.GetAnnotation(ctx, name, namespace, DNSControllerAnnotation)
	if err != nil {
		return false, err
	}

	if !exists {
		logger.V(1).Info("DNS controller annotation not found, assuming secondary")
		return false, nil
	}

	if value == DNSControllerEnabled {
		logger.V(1).Info("VirtualService is primary")
		return true, nil
	}

	logger.V(1).Info("VirtualService is secondary")
	return false, nil
}

// IsSecondary checks if the VirtualService is configured as secondary
func (m *Manager) IsSecondary(ctx context.Context, name, namespace string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("virtualservice", name, "namespace", namespace)
	logger.V(1).Info("Checking if VirtualService is secondary")

	isPrimary, err := m.IsPrimary(ctx, name, namespace)
	if err != nil {
		return false, err
	}

	return !isPrimary, nil
}

// IsReady checks if a VirtualService is ready
// For VirtualServices, we consider them ready as soon as they exist
// Since they don't have a status field like Ingresses
func (m *Manager) IsReady(ctx context.Context, name, namespace string) (bool, error) {
	logger := log.FromContext(ctx).WithValues("virtualservice", name, "namespace", namespace)
	logger.V(1).Info("Checking if VirtualService is ready")

	// Get the VirtualService - if it exists, it's considered ready
	_, err := m.getVirtualService(ctx, name, namespace)
	if err != nil {
		logger.Error(err, "Failed to get VirtualService")
		return false, err
	}

	logger.V(1).Info("VirtualService is ready")
	return true, nil
}

// WaitForReady waits for a VirtualService to be ready
func (m *Manager) WaitForReady(ctx context.Context, name, namespace string, timeoutSeconds int) error {
	logger := log.FromContext(ctx).WithValues("virtualservice", name, "namespace", namespace)
	logger.Info("Waiting for VirtualService to be ready", "timeout", timeoutSeconds)

	// Calculate deadline
	timeout := time.Duration(timeoutSeconds) * time.Second

	// Use exponential backoff to check
	backoff := wait.Backoff{
		Duration: 1 * time.Second,
		Factor:   1.5,
		Jitter:   0.1,
		Steps:    10,
		Cap:      30 * time.Second,
	}

	err := wait.ExponentialBackoff(backoff, func() (bool, error) {
		ready, err := m.IsReady(ctx, name, namespace)
		if err != nil {
			logger.Error(err, "Failed to check VirtualService readiness")
			return false, err
		}
		return ready, nil
	})

	if err == wait.ErrWaitTimeout {
		logger.Error(err, "Timeout waiting for VirtualService to be ready", "timeout", timeout)
		return fmt.Errorf("timeout waiting for VirtualService %s/%s to be ready after %v", namespace, name, timeout)
	}

	if err != nil {
		logger.Error(err, "Error while waiting for VirtualService to be ready")
		return err
	}

	logger.Info("VirtualService is ready")
	return nil
}

// WaitForAllVirtualServicesReady waits for all specified VirtualServices to be ready
func (m *Manager) WaitForAllVirtualServicesReady(ctx context.Context, namespace string, names []string, timeoutSeconds int) error {
	logger := log.FromContext(ctx).WithValues("namespace", namespace, "vsCount", len(names))
	logger.Info("Waiting for all VirtualServices to be ready", "timeout", timeoutSeconds)

	for _, name := range names {
		err := m.WaitForReady(ctx, name, namespace, timeoutSeconds)
		if err != nil {
			logger.Error(err, "Failed waiting for VirtualService", "virtualservice", name)
			return err
		}
	}

	logger.Info("All VirtualServices are ready")
	return nil
}
