package multicluster

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// ClusterManager defines an interface for managing multiple clusters
type ClusterManager interface {
	manager.Manager

	// GetCluster returns a cluster for the given name
	GetCluster(ctx context.Context, name string) (cluster.Cluster, error)

	// Engage registers a new cluster with the manager
	Engage(ctx context.Context, name string, cl cluster.Cluster) error

	// Disengage removes a cluster from the manager
	Disengage(ctx context.Context, name string) error

	// ListClusters returns a list of all registered clusters
	ListClusters() map[string]cluster.Cluster

	// ListClustersWithLog returns a list of all registered clusters and logs them
	ListClustersWithLog() map[string]cluster.Cluster
}

// MulticlusterManager is a wrapper around controller-runtime's manager.Manager
// that provides additional functionality for managing multiple clusters
type MulticlusterManager struct {
	manager.Manager
	clusters map[string]cluster.Cluster
	mu       sync.RWMutex
	log      logr.Logger
}

// NewMulticlusterManager creates a new MulticlusterManager
func NewMulticlusterManager(mgr manager.Manager) *MulticlusterManager {
	return &MulticlusterManager{
		Manager:  mgr,
		clusters: make(map[string]cluster.Cluster),
		log:      log.Log,
	}
}

// GetCluster returns a cluster.Cluster instance for the given name
func (m *MulticlusterManager) GetCluster(ctx context.Context, name string) (cluster.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Get the cluster
	cl, ok := m.clusters[name]
	if !ok {
		m.log.Error(nil, "Cluster not found", "name", name)
		return nil, fmt.Errorf("cluster %s not found", name)
	}

	return cl, nil
}

// ListClusters returns a map of cluster names to their cluster.Cluster instances
func (m *MulticlusterManager) ListClusters() map[string]cluster.Cluster {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Create a copy of the clusters map
	clusters := make(map[string]cluster.Cluster)
	for name, cl := range m.clusters {
		clusters[name] = cl
	}

	return clusters
}

// ListClustersWithLog logs the current list of clusters
func (m *MulticlusterManager) ListClustersWithLog() map[string]cluster.Cluster {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Log the current list of clusters
	m.log.Info("Current list of clusters", "count", len(m.clusters))
	for name := range m.clusters {
		m.log.Info("Cluster", "name", name)
	}

	// Return a copy of the clusters map
	clusters := make(map[string]cluster.Cluster)
	for name, cl := range m.clusters {
		clusters[name] = cl
	}

	return clusters
}

// Engage adds a cluster to the manager
func (m *MulticlusterManager) Engage(ctx context.Context, name string, cl cluster.Cluster) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Add the cluster to the manager
	if err := m.Manager.Add(cl); err != nil {
		m.log.Error(err, "Failed to add cluster to manager")
		return err
	}

	// Store the cluster in our internal state
	m.clusters[name] = cl

	m.log.Info("Successfully added cluster", "name", name)
	return nil
}

// Disengage removes a cluster from the manager
func (m *MulticlusterManager) Disengage(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Get the cluster
	_, ok := m.clusters[name]
	if !ok {
		return fmt.Errorf("cluster %s not found", name)
	}

	// Remove the cluster from our internal state
	delete(m.clusters, name)

	m.log.Info("Successfully removed cluster", "name", name)
	return nil
}
