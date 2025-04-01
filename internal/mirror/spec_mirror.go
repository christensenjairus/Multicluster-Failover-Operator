package mirror

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// specMirror implements SpecMirror interface
type specMirror struct {
	config *MirrorConfig
}

// NewSpecMirror creates a new SpecMirror
func NewSpecMirror(config *MirrorConfig) SpecMirror {
	return &specMirror{
		config: config,
	}
}

// SyncSpec implements SpecMirror.SyncSpec
func (m *specMirror) SyncSpec(ctx context.Context, obj MirrorableResource) error {
	// Get the last modified time of the incoming object
	incomingLastMod, err := GetLastModified(obj)
	if err != nil {
		return fmt.Errorf("failed to get last modified time: %w", err)
	}

	// Update last modified timestamp
	SetLastModified(obj)

	// Sync to all clusters
	for clusterName, cl := range m.config.Clusters {
		// Get the current version in the target cluster
		current := obj.DeepCopy()
		err := cl.Get(ctx, client.ObjectKeyFromObject(obj), current)
		if err != nil {
			if !errors.IsNotFound(err) {
				return fmt.Errorf("failed to get current version from cluster %s: %w", clusterName, err)
			}
			// If not found, create it
			if err := cl.Create(ctx, obj); err != nil {
				return fmt.Errorf("failed to create resource in cluster %s: %w", clusterName, err)
			}
			continue
		}

		// Get the last modified time of the current object
		currentLastMod, err := GetLastModified(current)
		if err != nil {
			return fmt.Errorf("failed to get current last modified time: %w", err)
		}

		// Only update if the incoming object is newer
		// TODO: In the future, we might want to add a more sophisticated conflict resolution strategy
		if !incomingLastMod.IsZero() && !currentLastMod.IsZero() && !incomingLastMod.After(currentLastMod) {
			continue
		}

		// Copy spec from incoming object
		current.SetSpec(obj.GetSpec())

		// Copy only our custom annotations
		incomingAnnotations := obj.GetAnnotations()
		currentAnnotations := current.GetAnnotations()
		if currentAnnotations == nil {
			currentAnnotations = make(map[string]string)
		}
		for k, v := range incomingAnnotations {
			if ShouldMirrorAnnotation(k) {
				currentAnnotations[k] = v
			}
		}
		current.SetAnnotations(currentAnnotations)

		// Update the resource
		if err := cl.Update(ctx, current); err != nil {
			return fmt.Errorf("failed to update resource in cluster %s: %w", clusterName, err)
		}
	}

	return nil
}
