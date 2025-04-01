package mirror

import (
	"context"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// LastModifiedAnnotation tracks when a resource was last modified
	LastModifiedAnnotation = "hahomelabs.com/last-modified"
	// LastStatusSyncAnnotation tracks when status was last synced from sot
	LastStatusSyncAnnotation = "hahomelabs.com/last-status-sync"
)

// MirrorableResource represents a resource that can be mirrored
type MirrorableResource interface {
	metav1.Object
	runtime.Object
	GetStatus() interface{}
	SetStatus(interface{})
	GetSpec() interface{}
	SetSpec(interface{})
	GetSourceOfTruthCluster() string
	DeepCopy() MirrorableResource
}

// StatusMirror handles one-way status mirroring from sot cluster to other clusters
type StatusMirror interface {
	// SyncStatus syncs status from sot cluster to all other clusters
	// Called periodically (every 10s) by the controller
	SyncStatus(ctx context.Context, obj MirrorableResource) error
}

// SpecMirror handles bidirectional spec and annotation mirroring between all clusters
type SpecMirror interface {
	// SyncSpec syncs spec and custom annotations between all clusters
	// Called immediately when changes are detected
	SyncSpec(ctx context.Context, obj MirrorableResource) error
}

// MirrorConfig holds configuration for the mirror implementations
type MirrorConfig struct {
	// Map of cluster names to clients
	Clusters map[string]client.Client
	// How often to sync status (default 10s)
	StatusSyncInterval time.Duration
}

// NewMirrorConfig creates a new MirrorConfig with default values
func NewMirrorConfig(clusters map[string]client.Client) *MirrorConfig {
	return &MirrorConfig{
		Clusters:           clusters,
		StatusSyncInterval: 10 * time.Second,
	}
}

// ShouldMirrorAnnotation returns true if the annotation should be mirrored
// Only mirrors hahomelabs.com/* annotations
func ShouldMirrorAnnotation(key string) bool {
	return strings.HasPrefix(key, "hahomelabs.com/")
}

// GetLastModified returns the last modified timestamp from annotations
func GetLastModified(obj metav1.Object) (time.Time, error) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return time.Time{}, nil
	}

	ts, ok := annotations[LastModifiedAnnotation]
	if !ok {
		return time.Time{}, nil
	}

	return time.Parse(time.RFC3339Nano, ts)
}

// SetLastModified sets the last modified timestamp annotation
func SetLastModified(obj metav1.Object) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[LastModifiedAnnotation] = time.Now().UTC().Format(time.RFC3339Nano)
	obj.SetAnnotations(annotations)
}

// GetLastStatusSync returns the last status sync timestamp from annotations
func GetLastStatusSync(obj metav1.Object) (time.Time, error) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return time.Time{}, nil
	}

	ts, ok := annotations[LastStatusSyncAnnotation]
	if !ok {
		return time.Time{}, nil
	}

	return time.Parse(time.RFC3339Nano, ts)
}

// SetLastStatusSync sets the last status sync timestamp annotation
func SetLastStatusSync(obj metav1.Object) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[LastStatusSyncAnnotation] = time.Now().UTC().Format(time.RFC3339Nano)
	obj.SetAnnotations(annotations)
}
