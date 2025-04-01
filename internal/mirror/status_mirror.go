package mirror

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// statusMirror implements StatusMirror interface
type statusMirror struct {
	config *MirrorConfig
}

// NewStatusMirror creates a new StatusMirror
func NewStatusMirror(config *MirrorConfig) StatusMirror {
	return &statusMirror{
		config: config,
	}
}

// SyncStatus implements StatusMirror.SyncStatus
func (m *statusMirror) SyncStatus(ctx context.Context, obj MirrorableResource) error {
	// Get the source of truth cluster from spec
	sotCluster := obj.GetSpec().(interface{ GetSourceOfTruthCluster() string }).GetSourceOfTruthCluster()
	if sotCluster == "" {
		return fmt.Errorf("resource has no source of truth cluster specified")
	}

	// Get the sot cluster client
	sotClient, ok := m.config.Clusters[sotCluster]
	if !ok {
		return fmt.Errorf("sot cluster %s not found", sotCluster)
	}

	// Get the latest version from the sot cluster
	latest := obj.DeepCopy()
	if err := sotClient.Get(ctx, client.ObjectKeyFromObject(obj), latest); err != nil {
		return fmt.Errorf("failed to get latest version from sot cluster: %w", err)
	}

	// Update the last status sync timestamp
	SetLastStatusSync(latest)

	// Sync to all other clusters
	for clusterName, cl := range m.config.Clusters {
		if clusterName == sotCluster {
			continue
		}

		// Get the current version in the target cluster
		current := obj.DeepCopy()
		err := cl.Get(ctx, client.ObjectKeyFromObject(obj), current)
		if err != nil {
			if !errors.IsNotFound(err) {
				return fmt.Errorf("failed to get current version from cluster %s: %w", clusterName, err)
			}
			// If not found, create it
			if err := cl.Create(ctx, latest); err != nil {
				return fmt.Errorf("failed to create resource in cluster %s: %w", clusterName, err)
			}
			continue
		}

		// Update the status in the target cluster
		// TODO: In the future, we might want to add a more sophisticated merge strategy
		current.SetStatus(latest.GetStatus())
		if err := cl.Status().Update(ctx, current); err != nil {
			return fmt.Errorf("failed to update status in cluster %s: %w", clusterName, err)
		}
	}

	return nil
}
