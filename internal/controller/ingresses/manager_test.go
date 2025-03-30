package ingresses

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// setupTestManager creates a test manager with a fake client
func setupTestManager() (*Manager, *networkingv1.Ingress) {
	// Create a scheme
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = networkingv1.AddToScheme(scheme)

	// Create a test Ingress
	pathType := networkingv1.PathTypePrefix
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ingress",
			Namespace: "default",
			Annotations: map[string]string{
				"annotation-key": "annotation-value",
			},
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: "example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "test-service",
											Port: networkingv1.ServiceBackendPort{
												Number: 80,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Create a fake client with the test Ingress
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ingress).
		Build()

	return NewManager(client), ingress
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

func TestUpdateIngress(t *testing.T) {
	// Setup
	manager, ingress := setupTestManager()
	ctx := context.Background()

	// Call the function to update the ingress to active state
	updated, err := manager.UpdateIngress(ctx, ingress.Name, ingress.Namespace, "active")

	// Verify operation succeeded
	assert.NoError(t, err)
	assert.True(t, updated, "Ingress should be marked as updated")

	// Get the updated ingress
	updatedIngress := &networkingv1.Ingress{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: ingress.Name, Namespace: ingress.Namespace}, updatedIngress)
	assert.NoError(t, err)

	// Verify annotations were set correctly for active state
	assert.Equal(t, DNSControllerEnabled, updatedIngress.Annotations[DNSControllerAnnotation])
	assert.Equal(t, DisabledValue, updatedIngress.Annotations[FluxReconcileAnnotation])

	// Test updating to passive state
	updated, err = manager.UpdateIngress(ctx, ingress.Name, ingress.Namespace, "passive")
	assert.NoError(t, err)
	assert.True(t, updated, "Ingress should be marked as updated")

	// Get the updated ingress again
	err = manager.client.Get(ctx, types.NamespacedName{Name: ingress.Name, Namespace: ingress.Namespace}, updatedIngress)
	assert.NoError(t, err)

	// Verify annotations were set correctly for passive state
	assert.Equal(t, DNSControllerDisabled, updatedIngress.Annotations[DNSControllerAnnotation])
	assert.Equal(t, DisabledValue, updatedIngress.Annotations[FluxReconcileAnnotation])
}

func TestProcessIngresses(t *testing.T) {
	// Setup
	manager, _ := setupTestManager()
	ctx := context.Background()
	names := []string{"test-ingress"}

	// Setup a second ingress for testing multiple ingresses
	secondIngress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-ingress2",
			Namespace:   "default",
			Annotations: map[string]string{},
		},
		Spec: networkingv1.IngressSpec{},
	}
	err := manager.client.Create(ctx, secondIngress)
	assert.NoError(t, err)
	names = append(names, "test-ingress2")

	// Test with active=true
	manager.ProcessIngresses(ctx, "default", names, true)

	// Verify all ingresses have been updated
	for _, name := range names {
		ingress := &networkingv1.Ingress{}
		err := manager.client.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, ingress)
		assert.NoError(t, err)
		assert.Equal(t, DNSControllerEnabled, ingress.Annotations[DNSControllerAnnotation])
		assert.Equal(t, DisabledValue, ingress.Annotations[FluxReconcileAnnotation])
	}

	// Test with active=false
	manager.ProcessIngresses(ctx, "default", names, false)

	// Verify all ingresses have been updated
	for _, name := range names {
		ingress := &networkingv1.Ingress{}
		err := manager.client.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, ingress)
		assert.NoError(t, err)
		assert.Equal(t, DNSControllerDisabled, ingress.Annotations[DNSControllerAnnotation])
		assert.Equal(t, DisabledValue, ingress.Annotations[FluxReconcileAnnotation])
	}
}

func TestAddFluxAnnotation(t *testing.T) {
	// Setup
	manager, ingress := setupTestManager()
	ctx := context.Background()

	// Call the function to add flux annotation
	err := manager.AddFluxAnnotation(ctx, ingress.Name, ingress.Namespace)

	// Verify operation succeeded
	assert.NoError(t, err)

	// Get the updated ingress
	updatedIngress := &networkingv1.Ingress{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: ingress.Name, Namespace: ingress.Namespace}, updatedIngress)
	assert.NoError(t, err)

	// Verify annotation was added
	assert.Equal(t, DisabledValue, updatedIngress.Annotations[FluxReconcileAnnotation])
}

func TestRemoveFluxAnnotation(t *testing.T) {
	// Setup
	manager, ingress := setupTestManager()
	ctx := context.Background()

	// First add the flux annotation to the test ingress
	ingress.Annotations[FluxReconcileAnnotation] = DisabledValue
	err := manager.client.Update(ctx, ingress)
	assert.NoError(t, err)

	// Call the function to remove flux annotation
	err = manager.RemoveFluxAnnotation(ctx, ingress.Name, ingress.Namespace)

	// Verify operation succeeded
	assert.NoError(t, err)

	// Get the updated ingress
	updatedIngress := &networkingv1.Ingress{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: ingress.Name, Namespace: ingress.Namespace}, updatedIngress)
	assert.NoError(t, err)

	// Verify annotation was removed
	_, exists := updatedIngress.Annotations[FluxReconcileAnnotation]
	assert.False(t, exists)
}

func TestAddAnnotation(t *testing.T) {
	// Setup
	manager, ingress := setupTestManager()
	ctx := context.Background()

	// Call the function to add annotation
	err := manager.AddAnnotation(ctx, ingress.Name, ingress.Namespace, "test-key", "test-value")

	// Verify operation succeeded
	assert.NoError(t, err)

	// Get the updated ingress
	updatedIngress := &networkingv1.Ingress{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: ingress.Name, Namespace: ingress.Namespace}, updatedIngress)
	assert.NoError(t, err)

	// Verify annotation was added
	assert.Equal(t, "test-value", updatedIngress.Annotations["test-key"])
}

func TestRemoveAnnotation(t *testing.T) {
	// Setup
	manager, ingress := setupTestManager()
	ctx := context.Background()

	// Add a test annotation
	ingress.Annotations["test-key"] = "test-value"
	err := manager.client.Update(ctx, ingress)
	assert.NoError(t, err)

	// Call the function to remove annotation
	err = manager.RemoveAnnotation(ctx, ingress.Name, ingress.Namespace, "test-key")

	// Verify operation succeeded
	assert.NoError(t, err)

	// Get the updated ingress
	updatedIngress := &networkingv1.Ingress{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: ingress.Name, Namespace: ingress.Namespace}, updatedIngress)
	assert.NoError(t, err)

	// Verify annotation was removed
	_, exists := updatedIngress.Annotations["test-key"]
	assert.False(t, exists)
}

func TestGetAnnotation(t *testing.T) {
	// Setup
	manager, ingress := setupTestManager()
	ctx := context.Background()

	// Add a test annotation
	const testKey = "test-key"
	const testValue = "test-value"
	ingress.Annotations[testKey] = testValue
	err := manager.client.Update(ctx, ingress)
	assert.NoError(t, err)

	// Call the function to get annotation
	value, exists, err := manager.GetAnnotation(ctx, ingress.Name, ingress.Namespace, testKey)

	// Verify operation succeeded and returned the correct values
	assert.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, testValue, value)

	// Test for non-existent annotation
	value, exists, err = manager.GetAnnotation(ctx, ingress.Name, ingress.Namespace, "nonexistent-key")
	assert.NoError(t, err)
	assert.False(t, exists)
	assert.Equal(t, "", value)
}

func TestSetDNSController(t *testing.T) {
	// Setup
	manager, ingress := setupTestManager()
	ctx := context.Background()

	// Test enable=true
	err := manager.SetDNSController(ctx, ingress.Name, ingress.Namespace, true)
	assert.NoError(t, err)

	// Get the updated ingress
	updatedIngress := &networkingv1.Ingress{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: ingress.Name, Namespace: ingress.Namespace}, updatedIngress)
	assert.NoError(t, err)

	// Verify annotation was set correctly
	assert.Equal(t, DNSControllerEnabled, updatedIngress.Annotations[DNSControllerAnnotation])

	// Test enable=false
	err = manager.SetDNSController(ctx, ingress.Name, ingress.Namespace, false)
	assert.NoError(t, err)

	// Get the updated ingress again
	err = manager.client.Get(ctx, types.NamespacedName{Name: ingress.Name, Namespace: ingress.Namespace}, updatedIngress)
	assert.NoError(t, err)

	// Verify annotation was set correctly
	assert.Equal(t, DNSControllerDisabled, updatedIngress.Annotations[DNSControllerAnnotation])
}

func TestIsPrimary(t *testing.T) {
	// Setup
	manager, ingress := setupTestManager()
	ctx := context.Background()

	// Test with annotation not set
	isPrimary, err := manager.IsPrimary(ctx, ingress.Name, ingress.Namespace)
	assert.NoError(t, err)
	assert.False(t, isPrimary)

	// Set DNS controller annotation to enabled
	ingress.Annotations[DNSControllerAnnotation] = DNSControllerEnabled
	err = manager.client.Update(ctx, ingress)
	assert.NoError(t, err)

	// Test with annotation set to enabled
	isPrimary, err = manager.IsPrimary(ctx, ingress.Name, ingress.Namespace)
	assert.NoError(t, err)
	assert.True(t, isPrimary)

	// Set DNS controller annotation to disabled
	ingress.Annotations[DNSControllerAnnotation] = DNSControllerDisabled
	err = manager.client.Update(ctx, ingress)
	assert.NoError(t, err)

	// Test with annotation set to disabled
	isPrimary, err = manager.IsPrimary(ctx, ingress.Name, ingress.Namespace)
	assert.NoError(t, err)
	assert.False(t, isPrimary)
}

func TestIsSecondary(t *testing.T) {
	// Setup
	manager, ingress := setupTestManager()
	ctx := context.Background()

	// Test with annotation not set
	isSecondary, err := manager.IsSecondary(ctx, ingress.Name, ingress.Namespace)
	assert.NoError(t, err)
	assert.True(t, isSecondary, "Should be secondary by default when annotation is not set")

	// Set DNS controller annotation to enabled
	ingress.Annotations[DNSControllerAnnotation] = DNSControllerEnabled
	err = manager.client.Update(ctx, ingress)
	assert.NoError(t, err)

	// Test with annotation set to enabled
	isSecondary, err = manager.IsSecondary(ctx, ingress.Name, ingress.Namespace)
	assert.NoError(t, err)
	assert.False(t, isSecondary, "Should not be secondary when annotation is enabled")

	// Set DNS controller annotation to disabled
	ingress.Annotations[DNSControllerAnnotation] = DNSControllerDisabled
	err = manager.client.Update(ctx, ingress)
	assert.NoError(t, err)

	// Test with annotation set to disabled
	isSecondary, err = manager.IsSecondary(ctx, ingress.Name, ingress.Namespace)
	assert.NoError(t, err)
	assert.True(t, isSecondary, "Should be secondary when annotation is disabled")
}

func TestIsReady(t *testing.T) {
	// Setup
	manager, ingress := setupTestManager()
	ctx := context.Background()

	// Test without status
	isReady, err := manager.IsReady(ctx, ingress.Name, ingress.Namespace)
	assert.NoError(t, err)
	assert.False(t, isReady, "Ingress without status should not be ready")

	// Add status with LoadBalancer ingress
	ingress.Status = networkingv1.IngressStatus{
		LoadBalancer: networkingv1.IngressLoadBalancerStatus{
			Ingress: []networkingv1.IngressLoadBalancerIngress{
				{
					IP: "192.168.1.1",
				},
			},
		},
	}
	err = manager.client.Status().Update(ctx, ingress)
	assert.NoError(t, err)

	// Test with status containing LoadBalancer ingress
	isReady, err = manager.IsReady(ctx, ingress.Name, ingress.Namespace)
	assert.NoError(t, err)
	assert.True(t, isReady, "Ingress with LoadBalancer status should be ready")
}

func TestWaitForReady(t *testing.T) {
	// Setup
	manager, ingress := setupTestManager()
	ctx := context.Background()

	// Add status with LoadBalancer ingress synchronously since this is a unit test
	// In a real environment, this would be set by the ingress controller
	ingress.Status = networkingv1.IngressStatus{
		LoadBalancer: networkingv1.IngressLoadBalancerStatus{
			Ingress: []networkingv1.IngressLoadBalancerIngress{
				{
					IP: "192.168.1.1",
				},
			},
		},
	}
	err := manager.client.Status().Update(ctx, ingress)
	assert.NoError(t, err)

	// Test the wait function with a timeout of 1 second
	err = manager.WaitForReady(ctx, ingress.Name, ingress.Namespace, 1)
	assert.NoError(t, err)
}

func TestWaitForAllIngressesReady(t *testing.T) {
	// Setup
	manager, ingress := setupTestManager()
	ctx := context.Background()

	// Create a second ingress
	secondIngress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-ingress2",
			Namespace:   "default",
			Annotations: map[string]string{},
		},
		Spec: networkingv1.IngressSpec{},
	}
	err := manager.client.Create(ctx, secondIngress)
	assert.NoError(t, err)

	// Update status for both ingresses synchronously
	ingress.Status = networkingv1.IngressStatus{
		LoadBalancer: networkingv1.IngressLoadBalancerStatus{
			Ingress: []networkingv1.IngressLoadBalancerIngress{
				{
					IP: "192.168.1.1",
				},
			},
		},
	}
	err = manager.client.Status().Update(ctx, ingress)
	assert.NoError(t, err)

	secondIngress.Status = networkingv1.IngressStatus{
		LoadBalancer: networkingv1.IngressLoadBalancerStatus{
			Ingress: []networkingv1.IngressLoadBalancerIngress{
				{
					IP: "192.168.1.2",
				},
			},
		},
	}
	err = manager.client.Status().Update(ctx, secondIngress)
	assert.NoError(t, err)

	// Test the wait function with a timeout of 1 second
	err = manager.WaitForAllIngressesReady(ctx, "default", []string{ingress.Name, secondIngress.Name}, 1)
	assert.NoError(t, err)
}
