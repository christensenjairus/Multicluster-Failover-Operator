package multicluster

import (
	"context"

	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
)

// MulticlusterReconcilerInterface defines an interface for accessing multiple clusters
type MulticlusterReconcilerInterface interface {
	// GetCluster returns a cluster for the given name
	GetCluster(ctx context.Context, name string) (cluster.Cluster, error)

	// ListClusters returns a list of all registered clusters
	ListClusters() map[string]cluster.Cluster

	// ListClustersWithLog returns a list of all registered clusters and logs them
	ListClustersWithLog() map[string]cluster.Cluster
}

// Provider defines an interface for cluster providers that can discover and manage clusters
type Provider interface {
	// Get returns the cluster with the given name, if it is known
	Get(ctx context.Context, clusterName string) (cluster.Cluster, error)

	// Run starts the provider and blocks, watching for cluster changes
	Run(ctx context.Context, mgr ClusterManager) error

	// IndexField indexes a field on all clusters, existing and future
	IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error

	// Engage creates, starts and registers a new cluster with the manager
	Engage(ctx context.Context, clusterName string, config *rest.Config) error

	// Disengage removes a cluster from the manager
	Disengage(ctx context.Context, clusterName string) error
}
