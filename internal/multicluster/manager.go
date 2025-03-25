package multicluster

import (
	"context"
	"fmt"
	"sync"

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

// MulticlusterManager is an implementation of the ClusterManager interface that manages
// multiple Kubernetes clusters.
type MulticlusterManager struct {
	manager.Manager
	clusters map[string]cluster.Cluster
	mu       sync.RWMutex
}

// NewMulticlusterManager creates a new MulticlusterManager.
func NewMulticlusterManager(mgr manager.Manager) *MulticlusterManager {
	return &MulticlusterManager{
		Manager:  mgr,
		clusters: make(map[string]cluster.Cluster),
	}
}

// GetCluster returns a cluster by name.
func (m *MulticlusterManager) GetCluster(ctx context.Context, name string) (cluster.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.clusters[name]
	if !ok {
		return nil, fmt.Errorf("cluster %s not found", name)
	}
	return c, nil
}

// Engage adds a cluster to the manager.
func (m *MulticlusterManager) Engage(ctx context.Context, name string, c cluster.Cluster) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clusters[name] = c
	log.FromContext(ctx).Info("registered cluster", "name", name)
	return nil
}

// Disengage removes a cluster from the manager.
func (m *MulticlusterManager) Disengage(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.clusters, name)
	log.FromContext(ctx).Info("unregistered cluster", "name", name)
	return nil
}

// ListClusters returns a list of all registered clusters
func (m *MulticlusterManager) ListClusters() map[string]cluster.Cluster {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.clusters == nil {
		return map[string]cluster.Cluster{}
	}

	// Return a copy of the map to prevent concurrent access
	clusters := make(map[string]cluster.Cluster, len(m.clusters))
	for name, cl := range m.clusters {
		clusters[name] = cl
	}
	return clusters
}

// ListClustersWithLog returns a list of all registered clusters and logs them
func (m *MulticlusterManager) ListClustersWithLog() map[string]cluster.Cluster {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.clusters == nil {
		log.Log.Info("no clusters registered")
		return map[string]cluster.Cluster{}
	}

	// Log all clusters
	clusterNames := make([]string, 0, len(m.clusters))
	for name := range m.clusters {
		clusterNames = append(clusterNames, name)
	}
	log.Log.Info("current registered clusters",
		"totalClusters", len(m.clusters),
		"clusterNames", clusterNames)

	// Return a copy of the map to prevent concurrent access
	clusters := make(map[string]cluster.Cluster, len(m.clusters))
	for name, cl := range m.clusters {
		clusters[name] = cl
	}
	return clusters
}
