package statefulsets

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// setupTestManager creates a test manager with a fake client and test StatefulSet
func setupTestManager() (*Manager, *appsv1.StatefulSet) {
	// Create a scheme
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	// Create a fake client
	client := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	// Create a test StatefulSet
	replicas := int32(3)
	statefulset := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-statefulset",
			Namespace: "default",
			Annotations: map[string]string{
				"test-key": "test-value",
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "test",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "test",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test-container",
							Image: "test-image:latest",
						},
					},
				},
			},
		},
		Status: appsv1.StatefulSetStatus{
			Replicas:        3,
			ReadyReplicas:   3,
			CurrentReplicas: 3,
		},
	}

	// Create the StatefulSet in the fake client
	_ = client.Create(context.Background(), statefulset)

	return NewManager(client), statefulset
}

// TestNewManager tests the creation of a new StatefulSet manager
func TestNewManager(t *testing.T) {
	// Create a fake client
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	// Create the manager
	manager := NewManager(client)

	// Assert manager is not nil and client is set
	assert.NotNil(t, manager)
	assert.Equal(t, client, manager.client)
}

// TestScaleStatefulSet tests scaling a StatefulSet to a specific number of replicas
func TestScaleStatefulSet(t *testing.T) {
	manager, statefulset := setupTestManager()
	ctx := context.Background()

	// Call the function to scale the StatefulSet to 0 replicas
	err := manager.ScaleStatefulSet(ctx, statefulset.Name, statefulset.Namespace, 0)
	assert.NoError(t, err)

	// Get the updated StatefulSet
	updatedStatefulSet := &appsv1.StatefulSet{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: statefulset.Name, Namespace: statefulset.Namespace}, updatedStatefulSet)
	assert.NoError(t, err)

	// Assert that the replicas were set to 0
	assert.Equal(t, int32(0), *updatedStatefulSet.Spec.Replicas)
}

// TestScaleDown tests scaling down a StatefulSet to 0 replicas
func TestScaleDown(t *testing.T) {
	manager, statefulset := setupTestManager()
	ctx := context.Background()

	// Call the function to scale down the StatefulSet
	err := manager.ScaleDown(ctx, statefulset.Name, statefulset.Namespace)
	assert.NoError(t, err)

	// Get the updated StatefulSet
	updatedStatefulSet := &appsv1.StatefulSet{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: statefulset.Name, Namespace: statefulset.Namespace}, updatedStatefulSet)
	assert.NoError(t, err)

	// Assert that the replicas were set to 0
	assert.Equal(t, int32(0), *updatedStatefulSet.Spec.Replicas)
}

// TestScaleUp tests scaling up a StatefulSet to a specific number of replicas
func TestScaleUp(t *testing.T) {
	manager, statefulset := setupTestManager()
	ctx := context.Background()

	// First scale down to 0
	replicas := int32(0)
	statefulset.Spec.Replicas = &replicas
	err := manager.client.Update(ctx, statefulset)
	assert.NoError(t, err)

	// Call the function to scale up the StatefulSet to 5 replicas
	err = manager.ScaleUp(ctx, statefulset.Name, statefulset.Namespace, 5)
	assert.NoError(t, err)

	// Get the updated StatefulSet
	updatedStatefulSet := &appsv1.StatefulSet{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: statefulset.Name, Namespace: statefulset.Namespace}, updatedStatefulSet)
	assert.NoError(t, err)

	// Assert that the replicas were set to 5
	assert.Equal(t, int32(5), *updatedStatefulSet.Spec.Replicas)
}

// TestGetCurrentReplicas tests getting the current replica count for a StatefulSet
func TestGetCurrentReplicas(t *testing.T) {
	manager, statefulset := setupTestManager()
	ctx := context.Background()

	// Call the function to get current replicas
	replicas, err := manager.GetCurrentReplicas(ctx, statefulset.Name, statefulset.Namespace)
	assert.NoError(t, err)

	// Assert that the correct replica count was returned
	assert.Equal(t, int32(3), replicas)
}

// TestIsReady tests checking if a StatefulSet is ready
func TestIsReady(t *testing.T) {
	manager, statefulset := setupTestManager()
	ctx := context.Background()

	// Call the function to check if the StatefulSet is ready
	ready, err := manager.IsReady(ctx, statefulset.Name, statefulset.Namespace)
	assert.NoError(t, err)

	// Assert that the StatefulSet is ready
	assert.True(t, ready)
}

// TestIsScaledDown tests checking if a StatefulSet is scaled down
func TestIsScaledDown(t *testing.T) {
	manager, statefulset := setupTestManager()
	ctx := context.Background()

	// Initially, the StatefulSet is not scaled down
	scaledDown, err := manager.IsScaledDown(ctx, statefulset.Name, statefulset.Namespace)
	assert.NoError(t, err)
	assert.False(t, scaledDown)

	// Scale down the StatefulSet
	replicas := int32(0)
	statefulset.Spec.Replicas = &replicas
	err = manager.client.Update(ctx, statefulset)
	assert.NoError(t, err)

	// Now the StatefulSet should be scaled down
	scaledDown, err = manager.IsScaledDown(ctx, statefulset.Name, statefulset.Namespace)
	assert.NoError(t, err)
	assert.True(t, scaledDown)
}

// TestAddAnnotation tests adding an annotation to a StatefulSet
func TestAddAnnotation(t *testing.T) {
	manager, statefulset := setupTestManager()
	ctx := context.Background()

	// Call the function to add an annotation
	err := manager.AddAnnotation(ctx, statefulset.Name, statefulset.Namespace, "test-annotation-key", "test-annotation-value")
	assert.NoError(t, err)

	// Get the updated StatefulSet
	updatedStatefulSet := &appsv1.StatefulSet{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: statefulset.Name, Namespace: statefulset.Namespace}, updatedStatefulSet)
	assert.NoError(t, err)

	// Assert that the annotation was added
	assert.Equal(t, "test-annotation-value", updatedStatefulSet.Annotations["test-annotation-key"])
}

// TestRemoveAnnotation tests removing an annotation from a StatefulSet
func TestRemoveAnnotation(t *testing.T) {
	manager, statefulset := setupTestManager()
	ctx := context.Background()

	// First add an annotation
	statefulset.Annotations["test-remove-key"] = "test-remove-value"
	err := manager.client.Update(ctx, statefulset)
	assert.NoError(t, err)

	// Call the function to remove the annotation
	err = manager.RemoveAnnotation(ctx, statefulset.Name, statefulset.Namespace, "test-remove-key")
	assert.NoError(t, err)

	// Get the updated StatefulSet
	updatedStatefulSet := &appsv1.StatefulSet{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: statefulset.Name, Namespace: statefulset.Namespace}, updatedStatefulSet)
	assert.NoError(t, err)

	// Assert that the annotation was removed
	_, exists := updatedStatefulSet.Annotations["test-remove-key"]
	assert.False(t, exists)
}

// TestGetAnnotation tests getting an annotation from a StatefulSet
func TestGetAnnotation(t *testing.T) {
	manager, statefulset := setupTestManager()
	ctx := context.Background()

	// Call the function to get an existing annotation
	value, exists, err := manager.GetAnnotation(ctx, statefulset.Name, statefulset.Namespace, "test-key")
	assert.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "test-value", value)

	// Call the function to get a non-existent annotation
	value, exists, err = manager.GetAnnotation(ctx, statefulset.Name, statefulset.Namespace, "non-existent-key")
	assert.NoError(t, err)
	assert.False(t, exists)
	assert.Equal(t, "", value)
}

// TestAddFluxAnnotation tests adding the Flux reconcile annotation to a StatefulSet
func TestAddFluxAnnotation(t *testing.T) {
	manager, statefulset := setupTestManager()
	ctx := context.Background()

	// Call the function to add the Flux annotation
	err := manager.AddFluxAnnotation(ctx, statefulset.Name, statefulset.Namespace)
	assert.NoError(t, err)

	// Get the updated StatefulSet
	updatedStatefulSet := &appsv1.StatefulSet{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: statefulset.Name, Namespace: statefulset.Namespace}, updatedStatefulSet)
	assert.NoError(t, err)

	// Assert that the Flux annotation was added
	assert.Equal(t, DisabledValue, updatedStatefulSet.Annotations[FluxReconcileAnnotation])
}

// TestRemoveFluxAnnotation tests removing the Flux reconcile annotation from a StatefulSet
func TestRemoveFluxAnnotation(t *testing.T) {
	manager, statefulset := setupTestManager()
	ctx := context.Background()

	// First add the Flux annotation
	if statefulset.Annotations == nil {
		statefulset.Annotations = make(map[string]string)
	}
	statefulset.Annotations[FluxReconcileAnnotation] = DisabledValue
	err := manager.client.Update(ctx, statefulset)
	assert.NoError(t, err)

	// Verify the annotation was added
	updatedStatefulSet := &appsv1.StatefulSet{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: statefulset.Name, Namespace: statefulset.Namespace}, updatedStatefulSet)
	assert.NoError(t, err)
	assert.Equal(t, DisabledValue, updatedStatefulSet.Annotations[FluxReconcileAnnotation])

	// Call the function to remove the Flux annotation
	err = manager.RemoveFluxAnnotation(ctx, statefulset.Name, statefulset.Namespace)
	assert.NoError(t, err)

	// Get the updated StatefulSet again
	updatedStatefulSet = &appsv1.StatefulSet{}
	err = manager.client.Get(ctx, types.NamespacedName{Name: statefulset.Name, Namespace: statefulset.Namespace}, updatedStatefulSet)
	assert.NoError(t, err)

	// Assert that the Flux annotation was removed
	_, exists := updatedStatefulSet.Annotations[FluxReconcileAnnotation]
	assert.False(t, exists)
}

// TestWaitForReplicasReady tests waiting for all replicas of a StatefulSet to be ready
func TestWaitForReplicasReady(t *testing.T) {
	manager, statefulset := setupTestManager()
	ctx := context.Background()

	// Call the function to wait for replicas to be ready
	// Since our mock StatefulSet is already ready, this should return immediately
	err := manager.WaitForReplicasReady(ctx, statefulset.Name, statefulset.Namespace, 1*time.Second)
	assert.NoError(t, err)
}

// TestProcessStatefulSets tests processing multiple StatefulSets
func TestProcessStatefulSets(t *testing.T) {
	manager, statefulset := setupTestManager()
	ctx := context.Background()

	// Create a second StatefulSet
	statefulset2 := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-statefulset-2",
			Namespace: "default",
			Annotations: map[string]string{
				"test-key": "test-value",
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: statefulset.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "test-2",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "test-2",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test-container",
							Image: "test-image:latest",
						},
					},
				},
			},
		},
		Status: appsv1.StatefulSetStatus{
			Replicas:        3,
			ReadyReplicas:   3,
			CurrentReplicas: 3,
		},
	}

	err := manager.client.Create(ctx, statefulset2)
	assert.NoError(t, err)

	// Process StatefulSets - scaling down
	names := []string{statefulset.Name, statefulset2.Name}
	manager.ProcessStatefulSets(ctx, statefulset.Namespace, names, false, 0)

	// Verify both StatefulSets are scaled down
	for _, name := range names {
		sts := &appsv1.StatefulSet{}
		err := manager.client.Get(ctx, types.NamespacedName{Name: name, Namespace: statefulset.Namespace}, sts)
		assert.NoError(t, err)
		assert.Equal(t, int32(0), *sts.Spec.Replicas)
	}

	// Process StatefulSets - scaling up
	manager.ProcessStatefulSets(ctx, statefulset.Namespace, names, true, 2)

	// Verify both StatefulSets are scaled up
	for _, name := range names {
		sts := &appsv1.StatefulSet{}
		err := manager.client.Get(ctx, types.NamespacedName{Name: name, Namespace: statefulset.Namespace}, sts)
		assert.NoError(t, err)
		assert.Equal(t, int32(2), *sts.Spec.Replicas)
	}
}

// TestWaitForAllStatefulSetsReady tests waiting for all StatefulSets to be ready
func TestWaitForAllStatefulSetsReady(t *testing.T) {
	manager, statefulset := setupTestManager()
	ctx := context.Background()

	// Create a second StatefulSet
	statefulset2 := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-statefulset-2",
			Namespace: "default",
			Annotations: map[string]string{
				"test-key": "test-value",
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: statefulset.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "test-2",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "test-2",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test-container",
							Image: "test-image:latest",
						},
					},
				},
			},
		},
		Status: appsv1.StatefulSetStatus{
			Replicas:        3,
			ReadyReplicas:   3,
			CurrentReplicas: 3,
		},
	}

	err := manager.client.Create(ctx, statefulset2)
	assert.NoError(t, err)

	// Call the function to wait for all StatefulSets to be ready
	names := []string{statefulset.Name, statefulset2.Name}
	err = manager.WaitForAllStatefulSetsReady(ctx, statefulset.Namespace, names, 1*time.Second)
	assert.NoError(t, err)
}
