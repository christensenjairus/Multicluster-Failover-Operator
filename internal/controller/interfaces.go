package controller

import (
	"context"

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
