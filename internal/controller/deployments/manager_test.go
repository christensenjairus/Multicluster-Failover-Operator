package deployments

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func setupTestManager() *Manager {
	// Create a fake client
	client := fake.NewClientBuilder().Build()
	return NewManager(client)
}

func setupTestDeployments() (*Manager, []*appsv1.Deployment) {
	// Create a scheme
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	// Create test deployments
	deployments := []*appsv1.Deployment{}
	for i := 1; i <= 3; i++ {
		replicas := int32(1)
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("test-deployment-%d", i),
				Namespace: "test-namespace",
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
			},
			Status: appsv1.DeploymentStatus{
				Replicas:           1,
				AvailableReplicas:  1,
				ReadyReplicas:      1,
				UpdatedReplicas:    1,
				ObservedGeneration: 1,
			},
		}
		deployments = append(deployments, deployment)
	}

	// Create a fake client with the deployments
	clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
	for _, deployment := range deployments {
		clientBuilder = clientBuilder.WithObjects(deployment)
	}

	client := clientBuilder.Build()
	return NewManager(client), deployments
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

func TestScaleDeployment(t *testing.T) {
	// Setup
	manager, deployments := setupTestDeployments()
	ctx := context.Background()
	deployment := deployments[0]

	// Call the function
	err := manager.ScaleDeployment(ctx, deployment.Name, deployment.Namespace, 3)

	// Assert
	assert.NoError(t, err)

	// Verify the deployment was scaled
	updatedDeployment := &appsv1.Deployment{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, updatedDeployment)
	assert.NoError(t, err)
	assert.Equal(t, int32(3), *updatedDeployment.Spec.Replicas)
}

func TestScaleDown(t *testing.T) {
	// Setup
	manager, deployments := setupTestDeployments()
	ctx := context.Background()
	deployment := deployments[0]

	// Call the function
	err := manager.ScaleDown(ctx, deployment.Name, deployment.Namespace)

	// Assert
	assert.NoError(t, err)

	// Verify the deployment was scaled down
	updatedDeployment := &appsv1.Deployment{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, updatedDeployment)
	assert.NoError(t, err)
	assert.Equal(t, int32(0), *updatedDeployment.Spec.Replicas)
}

func TestScaleUp(t *testing.T) {
	// Setup
	manager, deployments := setupTestDeployments()
	ctx := context.Background()
	deployment := deployments[0]

	// Call the function
	err := manager.ScaleUp(ctx, deployment.Name, deployment.Namespace, 3)

	// Assert
	assert.NoError(t, err)

	// Verify the deployment was scaled up
	updatedDeployment := &appsv1.Deployment{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, updatedDeployment)
	assert.NoError(t, err)
	assert.Equal(t, int32(3), *updatedDeployment.Spec.Replicas)
}

func TestGetCurrentReplicas(t *testing.T) {
	// Setup
	manager, deployments := setupTestDeployments()
	ctx := context.Background()
	deployment := deployments[0]

	// Call the function
	replicas, err := manager.GetCurrentReplicas(ctx, deployment.Name, deployment.Namespace)

	// Assert
	assert.NoError(t, err)
	assert.Equal(t, int32(1), replicas)

	// Update the replica count and test again
	newReplicas := int32(3)
	deployment.Spec.Replicas = &newReplicas
	err = manager.client.Update(ctx, deployment)
	assert.NoError(t, err)

	// Get updated replicas
	updatedReplicas, err := manager.GetCurrentReplicas(ctx, deployment.Name, deployment.Namespace)
	assert.NoError(t, err)
	assert.Equal(t, int32(3), updatedReplicas)
}

func TestWaitForReplicasReady(t *testing.T) {
	// Setup
	manager, deployments := setupTestDeployments()
	ctx := context.Background()
	deployment := deployments[0]

	// Call the function
	err := manager.WaitForReplicasReady(ctx, deployment.Name, deployment.Namespace, 5)

	// Assert
	assert.NoError(t, err)

	// Set up a non-ready deployment to test failure case
	replicas := int32(2)
	deployment.Spec.Replicas = &replicas
	// Only 1 replica is available, not matching the desired 2
	deployment.Status.AvailableReplicas = 1
	err = manager.client.Update(ctx, deployment)
	assert.NoError(t, err)

	// Use a very short timeout to ensure test completes quickly
	err = manager.WaitForReplicasReady(ctx, deployment.Name, deployment.Namespace, 1)
	assert.Error(t, err)
}

func TestIsReady(t *testing.T) {
	// Setup
	manager, deployments := setupTestDeployments()
	ctx := context.Background()
	deployment := deployments[0]

	// Call the function on a ready deployment
	ready, err := manager.IsReady(ctx, deployment.Name, deployment.Namespace)

	// Assert
	assert.NoError(t, err)
	assert.True(t, ready)

	// Update to an unready state
	replicas := int32(2)
	deployment.Spec.Replicas = &replicas
	// Only 1 replica is available, not matching the desired 2
	deployment.Status.AvailableReplicas = 1
	err = manager.client.Update(ctx, deployment)
	assert.NoError(t, err)

	// Test unready deployment
	ready, err = manager.IsReady(ctx, deployment.Name, deployment.Namespace)
	assert.NoError(t, err)
	assert.False(t, ready)
}

func TestIsScaledDown(t *testing.T) {
	// Setup
	manager, deployments := setupTestDeployments()
	ctx := context.Background()
	deployment := deployments[0]

	// Initially deployment has 1 replica
	scaledDown, err := manager.IsScaledDown(ctx, deployment.Name, deployment.Namespace)
	assert.NoError(t, err)
	assert.False(t, scaledDown)

	// Scale down to 0
	replicas := int32(0)
	deployment.Spec.Replicas = &replicas
	err = manager.client.Update(ctx, deployment)
	assert.NoError(t, err)

	// Test scaled down deployment
	scaledDown, err = manager.IsScaledDown(ctx, deployment.Name, deployment.Namespace)
	assert.NoError(t, err)
	assert.True(t, scaledDown)
}

func TestAddFluxAnnotation(t *testing.T) {
	// Setup
	manager, deployments := setupTestDeployments()
	ctx := context.Background()
	deployment := deployments[0]

	// Call the function
	err := manager.AddFluxAnnotation(ctx, deployment.Name, deployment.Namespace)

	// Assert
	assert.NoError(t, err)

	// Verify the annotation was added
	updatedDeployment := &appsv1.Deployment{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, updatedDeployment)
	assert.NoError(t, err)
	assert.Equal(t, "disabled", updatedDeployment.Annotations[FluxReconcileAnnotation])
}

func TestRemoveFluxAnnotation(t *testing.T) {
	// Setup
	manager, deployments := setupTestDeployments()
	ctx := context.Background()
	deployment := deployments[0]

	// Add the flux annotation first
	if deployment.Annotations == nil {
		deployment.Annotations = make(map[string]string)
	}
	deployment.Annotations[FluxReconcileAnnotation] = DisabledValue
	err := manager.client.Update(ctx, deployment)
	assert.NoError(t, err)

	// Call the function to remove it
	err = manager.RemoveFluxAnnotation(ctx, deployment.Name, deployment.Namespace)
	assert.NoError(t, err)

	// Verify the annotation was removed
	updatedDeployment := &appsv1.Deployment{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, updatedDeployment)
	assert.NoError(t, err)
	_, exists := updatedDeployment.Annotations[FluxReconcileAnnotation]
	assert.False(t, exists)
}

func TestAddAnnotation(t *testing.T) {
	// Setup
	manager, deployments := setupTestDeployments()
	ctx := context.Background()
	deployment := deployments[0]

	// Call the function
	err := manager.AddAnnotation(ctx, deployment.Name, deployment.Namespace, "test-key", "test-value")

	// Assert
	assert.NoError(t, err)

	// Verify the annotation was added
	updatedDeployment := &appsv1.Deployment{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, updatedDeployment)
	assert.NoError(t, err)
	assert.Equal(t, "test-value", updatedDeployment.Annotations["test-key"])
}

func TestRemoveAnnotation(t *testing.T) {
	// Setup
	manager, deployments := setupTestDeployments()
	ctx := context.Background()
	deployment := deployments[0]

	// Add an annotation first
	if deployment.Annotations == nil {
		deployment.Annotations = make(map[string]string)
	}
	deployment.Annotations["test-key"] = "test-value"
	err := manager.client.Update(ctx, deployment)
	assert.NoError(t, err)

	// Call the function to remove it
	err = manager.RemoveAnnotation(ctx, deployment.Name, deployment.Namespace, "test-key")
	assert.NoError(t, err)

	// Verify the annotation was removed
	updatedDeployment := &appsv1.Deployment{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, updatedDeployment)
	assert.NoError(t, err)
	_, exists := updatedDeployment.Annotations["test-key"]
	assert.False(t, exists)
}

func TestGetAnnotation(t *testing.T) {
	// Setup
	manager, deployments := setupTestDeployments()
	ctx := context.Background()
	deployment := deployments[0]

	// Add an annotation first
	if deployment.Annotations == nil {
		deployment.Annotations = make(map[string]string)
	}
	deployment.Annotations["test-key"] = "test-value"
	err := manager.client.Update(ctx, deployment)
	assert.NoError(t, err)

	// Call the function
	value, exists, err := manager.GetAnnotation(ctx, deployment.Name, deployment.Namespace, "test-key")

	// Assert
	assert.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "test-value", value)

	// Test non-existent annotation
	value, exists, err = manager.GetAnnotation(ctx, deployment.Name, deployment.Namespace, "non-existent-key")
	assert.NoError(t, err)
	assert.False(t, exists)
	assert.Equal(t, "", value)
}

// TestProcessDeployments tests processing multiple Deployments
func TestProcessDeployments(t *testing.T) {
	// Setup
	manager, deployments := setupTestDeployments()
	ctx := context.Background()

	// Get deployment names
	deploymentNames := []string{}
	for _, deployment := range deployments {
		deploymentNames = append(deploymentNames, deployment.Name)
	}
	namespace := "test-namespace"

	// Test scaling down deployments (active = false)
	manager.ProcessDeployments(ctx, namespace, deploymentNames, false, 0)

	// Verify all deployments are scaled down
	for _, name := range deploymentNames {
		updatedDeployment := &appsv1.Deployment{}
		err := manager.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, updatedDeployment)
		assert.NoError(t, err)
		assert.Equal(t, int32(0), *updatedDeployment.Spec.Replicas, "Deployment %s should be scaled down to 0 replicas", name)
	}

	// Test scaling up deployments (active = true)
	targetReplicas := int32(3)
	manager.ProcessDeployments(ctx, namespace, deploymentNames, true, targetReplicas)

	// Verify all deployments are scaled up to the target replica count
	for _, name := range deploymentNames {
		updatedDeployment := &appsv1.Deployment{}
		err := manager.client.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, updatedDeployment)
		assert.NoError(t, err)
		assert.Equal(t, targetReplicas, *updatedDeployment.Spec.Replicas, "Deployment %s should be scaled up to %d replicas", name, targetReplicas)
	}
}

// TestWaitForAllDeploymentsState tests waiting for all Deployments to reach a desired state
func TestWaitForAllDeploymentsState(t *testing.T) {
	// Setup
	manager, deployments := setupTestDeployments()
	ctx := context.Background()

	// Get deployment names
	deploymentNames := []string{}
	for _, deployment := range deployments {
		deploymentNames = append(deploymentNames, deployment.Name)
	}
	namespace := "test-namespace"
	timeout := 5 * time.Second

	// First scale down the deployments to set up for the test
	for _, deployment := range deployments {
		replicas := int32(0)
		deployment.Spec.Replicas = &replicas
		deployment.Status.Replicas = 0
		deployment.Status.AvailableReplicas = 0
		deployment.Status.ReadyReplicas = 0
		err := manager.client.Update(ctx, deployment)
		assert.NoError(t, err)
	}

	// Test waiting for deployments to be scaled down
	err := manager.WaitForAllDeploymentsState(ctx, namespace, deploymentNames, true, timeout)
	assert.NoError(t, err)

	// Now scale up the deployments to set up for the next test
	for _, deployment := range deployments {
		replicas := int32(1)
		deployment.Spec.Replicas = &replicas
		deployment.Status.Replicas = 1
		deployment.Status.AvailableReplicas = 1
		deployment.Status.ReadyReplicas = 1
		err := manager.client.Update(ctx, deployment)
		assert.NoError(t, err)
	}

	// Test waiting for deployments to be ready
	err = manager.WaitForAllDeploymentsState(ctx, namespace, deploymentNames, false, timeout)
	assert.NoError(t, err)

	// Test with a non-existent deployment to verify error handling
	nonExistentNames := append(deploymentNames, "non-existent-deployment")
	err = manager.WaitForAllDeploymentsState(ctx, namespace, nonExistentNames, false, 2*time.Second)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "non-existent-deployment")
}
