package controller

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/christensenjairus/Multicluster-Failover-Operator/internal/mirror"
	kubeconfigprovider "sigs.k8s.io/multicluster-runtime/providers/kubeconfig"
)

// SetupResourceMirroring sets up mirroring for a specific resource type in a cluster
func SetupResourceMirroring(ctx context.Context, provider *kubeconfigprovider.Provider, cl cluster.Cluster, clusterName string, objType client.Object) error {
	log := log.FromContext(ctx).WithName("mirror-setup").WithValues("cluster", clusterName)

	// Create a resource mirror
	resourceMirror := mirror.NewResourceMirror(provider)

	// Set up the watch for the resource type
	if err := resourceMirror.SetupMirrorWatch(ctx, clusterName, cl, objType); err != nil {
		log.Error(err, "Failed to set up resource mirror watch")
		return err
	}

	return nil
}
