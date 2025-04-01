package mirror

import (
	"fmt"
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	crdv1alpha1 "github.com/christensenjairus/Multicluster-Failover-Operator/api/v1alpha1"
)

// ResourceWrapper wraps a runtime.Object to implement MirrorableResource
type ResourceWrapper struct {
	obj runtime.Object
}

// NewResourceWrapper creates a new ResourceWrapper
func NewResourceWrapper(obj runtime.Object) (*ResourceWrapper, error) {
	if _, ok := obj.(metav1.Object); !ok {
		return nil, fmt.Errorf("object does not implement metav1.Object")
	}

	return &ResourceWrapper{
		obj: obj,
	}, nil
}

// GetAnnotations implements metav1.Object
func (w *ResourceWrapper) GetAnnotations() map[string]string {
	return w.obj.(metav1.Object).GetAnnotations()
}

// SetAnnotations implements metav1.Object
func (w *ResourceWrapper) SetAnnotations(annotations map[string]string) {
	w.obj.(metav1.Object).SetAnnotations(annotations)
}

// GetName implements metav1.Object
func (w *ResourceWrapper) GetName() string {
	return w.obj.(metav1.Object).GetName()
}

// SetName implements metav1.Object
func (w *ResourceWrapper) SetName(name string) {
	w.obj.(metav1.Object).SetName(name)
}

// GetNamespace implements metav1.Object
func (w *ResourceWrapper) GetNamespace() string {
	return w.obj.(metav1.Object).GetNamespace()
}

// SetNamespace implements metav1.Object
func (w *ResourceWrapper) SetNamespace(namespace string) {
	w.obj.(metav1.Object).SetNamespace(namespace)
}

// GetLabels implements metav1.Object
func (w *ResourceWrapper) GetLabels() map[string]string {
	return w.obj.(metav1.Object).GetLabels()
}

// SetLabels implements metav1.Object
func (w *ResourceWrapper) SetLabels(labels map[string]string) {
	w.obj.(metav1.Object).SetLabels(labels)
}

// GetResourceVersion implements metav1.Object
func (w *ResourceWrapper) GetResourceVersion() string {
	return w.obj.(metav1.Object).GetResourceVersion()
}

// SetResourceVersion implements metav1.Object
func (w *ResourceWrapper) SetResourceVersion(version string) {
	w.obj.(metav1.Object).SetResourceVersion(version)
}

// GetGeneration implements metav1.Object
func (w *ResourceWrapper) GetGeneration() int64 {
	return w.obj.(metav1.Object).GetGeneration()
}

// SetGeneration implements metav1.Object
func (w *ResourceWrapper) SetGeneration(generation int64) {
	w.obj.(metav1.Object).SetGeneration(generation)
}

// GetDeletionTimestamp implements metav1.Object
func (w *ResourceWrapper) GetDeletionTimestamp() *metav1.Time {
	return w.obj.(metav1.Object).GetDeletionTimestamp()
}

// SetDeletionTimestamp implements metav1.Object
func (w *ResourceWrapper) SetDeletionTimestamp(timestamp *metav1.Time) {
	w.obj.(metav1.Object).SetDeletionTimestamp(timestamp)
}

// GetDeletionGracePeriodSeconds implements metav1.Object
func (w *ResourceWrapper) GetDeletionGracePeriodSeconds() *int64 {
	return w.obj.(metav1.Object).GetDeletionGracePeriodSeconds()
}

// SetDeletionGracePeriodSeconds implements metav1.Object
func (w *ResourceWrapper) SetDeletionGracePeriodSeconds(seconds *int64) {
	w.obj.(metav1.Object).SetDeletionGracePeriodSeconds(seconds)
}

// GetFinalizers implements metav1.Object
func (w *ResourceWrapper) GetFinalizers() []string {
	return w.obj.(metav1.Object).GetFinalizers()
}

// SetFinalizers implements metav1.Object
func (w *ResourceWrapper) SetFinalizers(finalizers []string) {
	w.obj.(metav1.Object).SetFinalizers(finalizers)
}

// GetOwnerReferences implements metav1.Object
func (w *ResourceWrapper) GetOwnerReferences() []metav1.OwnerReference {
	return w.obj.(metav1.Object).GetOwnerReferences()
}

// SetOwnerReferences implements metav1.Object
func (w *ResourceWrapper) SetOwnerReferences(refs []metav1.OwnerReference) {
	w.obj.(metav1.Object).SetOwnerReferences(refs)
}

// GetManagedFields implements metav1.Object
func (w *ResourceWrapper) GetManagedFields() []metav1.ManagedFieldsEntry {
	return w.obj.(metav1.Object).GetManagedFields()
}

// SetManagedFields implements metav1.Object
func (w *ResourceWrapper) SetManagedFields(fields []metav1.ManagedFieldsEntry) {
	w.obj.(metav1.Object).SetManagedFields(fields)
}

// DeepCopyObject implements runtime.Object
func (w *ResourceWrapper) DeepCopyObject() runtime.Object {
	return &ResourceWrapper{
		obj: w.obj.DeepCopyObject(),
	}
}

// DeepCopy implements MirrorableResource
func (w *ResourceWrapper) DeepCopy() MirrorableResource {
	return &ResourceWrapper{
		obj: w.obj.DeepCopyObject(),
	}
}

// GetSpec implements MirrorableResource
func (w *ResourceWrapper) GetSpec() interface{} {
	val := reflect.ValueOf(w.obj)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	spec := val.FieldByName("Spec")
	if !spec.IsValid() {
		return nil
	}
	return spec.Interface()
}

// SetSpec implements MirrorableResource
func (w *ResourceWrapper) SetSpec(spec interface{}) {
	val := reflect.ValueOf(w.obj)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	specField := val.FieldByName("Spec")
	if !specField.IsValid() {
		return
	}
	specField.Set(reflect.ValueOf(spec))
}

// GetStatus implements MirrorableResource
func (w *ResourceWrapper) GetStatus() interface{} {
	val := reflect.ValueOf(w.obj)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	status := val.FieldByName("Status")
	if !status.IsValid() {
		return nil
	}
	return status.Interface()
}

// SetStatus implements MirrorableResource
func (w *ResourceWrapper) SetStatus(status interface{}) {
	val := reflect.ValueOf(w.obj)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	statusField := val.FieldByName("Status")
	if !statusField.IsValid() {
		return
	}
	statusField.Set(reflect.ValueOf(status))
}

// GetObjectKind implements runtime.Object
func (w *ResourceWrapper) GetObjectKind() schema.ObjectKind {
	return w.obj.GetObjectKind()
}

// GetCreationTimestamp implements metav1.Object
func (w *ResourceWrapper) GetCreationTimestamp() metav1.Time {
	return w.obj.(metav1.Object).GetCreationTimestamp()
}

// SetCreationTimestamp implements metav1.Object
func (w *ResourceWrapper) SetCreationTimestamp(timestamp metav1.Time) {
	w.obj.(metav1.Object).SetCreationTimestamp(timestamp)
}

// GetGenerateName implements metav1.Object
func (w *ResourceWrapper) GetGenerateName() string {
	return w.obj.(metav1.Object).GetGenerateName()
}

// SetGenerateName implements metav1.Object
func (w *ResourceWrapper) SetGenerateName(name string) {
	w.obj.(metav1.Object).SetGenerateName(name)
}

// GetSelfLink implements metav1.Object
func (w *ResourceWrapper) GetSelfLink() string {
	return w.obj.(metav1.Object).GetSelfLink()
}

// SetSelfLink implements metav1.Object
func (w *ResourceWrapper) SetSelfLink(selfLink string) {
	w.obj.(metav1.Object).SetSelfLink(selfLink)
}

// GetUID implements metav1.Object
func (w *ResourceWrapper) GetUID() types.UID {
	return w.obj.(metav1.Object).GetUID()
}

// SetUID implements metav1.Object
func (w *ResourceWrapper) SetUID(uid types.UID) {
	w.obj.(metav1.Object).SetUID(uid)
}

// GetSourceOfTruthCluster returns the source of truth cluster from the spec
func (w *ResourceWrapper) GetSourceOfTruthCluster() string {
	switch r := w.obj.(type) {
	case *crdv1alpha1.Failover:
		return r.Spec.SourceOfTruthCluster
	case *crdv1alpha1.FailoverGroup:
		return r.Spec.SourceOfTruthCluster
	default:
		return ""
	}
}
