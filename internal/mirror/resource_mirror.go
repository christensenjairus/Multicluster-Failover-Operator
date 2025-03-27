package mirror

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kubeconfigprovider "sigs.k8s.io/multicluster-runtime/providers/kubeconfig"
)

// ResourceMirror handles cross-cluster resource mirroring
type ResourceMirror struct {
	Log      logr.Logger
	Provider *kubeconfigprovider.Provider
}

// NewResourceMirror creates a new ResourceMirror
func NewResourceMirror(provider *kubeconfigprovider.Provider) *ResourceMirror {
	return &ResourceMirror{
		Log:      log.FromContext(context.Background()).WithName("resource-mirror"),
		Provider: provider,
	}
}

// MirrorObject mirrors an object to all other clusters
func (m *ResourceMirror) MirrorObject(ctx context.Context, sourceClusterName string, obj client.Object) error {
	log := m.Log.WithValues(
		"kind", obj.GetObjectKind().GroupVersionKind().Kind,
		"name", obj.GetName(),
		"namespace", obj.GetNamespace(),
		"sourceCluster", sourceClusterName,
	)

	// Get all clusters
	clusters := m.Provider.ListClusters()

	// Create a deep copy of the object
	mirroredObj := obj.DeepCopyObject().(client.Object)

	// Mirror to each cluster
	for clusterName, targetCluster := range clusters {
		// Skip the source cluster
		if clusterName == sourceClusterName {
			continue
		}

		log.V(2).Info("Mirroring object to cluster", "targetCluster", clusterName)

		// Check if object exists in target cluster
		existingObj := obj.DeepCopyObject().(client.Object)
		err := targetCluster.GetClient().Get(ctx, client.ObjectKey{
			Namespace: obj.GetNamespace(),
			Name:      obj.GetName(),
		}, existingObj)

		if err != nil {
			if client.IgnoreNotFound(err) != nil {
				log.V(2).Error(err, "Failed to check if object exists in target cluster", "targetCluster", clusterName)
				continue
			}
			// Object doesn't exist, create it
			// Clear UID and resourceVersion for new objects
			mirroredObj.SetUID("")
			mirroredObj.SetResourceVersion("")
			if err := targetCluster.GetClient().Create(ctx, mirroredObj); err != nil {
				log.V(2).Error(err, "Failed to create mirrored object in target cluster", "targetCluster", clusterName)
				continue
			}
		} else {
			// Object exists, update it while preserving resourceVersion and UID
			mirroredObj.SetResourceVersion(existingObj.GetResourceVersion())
			mirroredObj.SetUID(existingObj.GetUID())
			if err := targetCluster.GetClient().Update(ctx, mirroredObj); err != nil {
				// Ignore concurrent modification errors as they are expected in mirroring scenarios
				if !errors.IsConflict(err) {
					log.V(2).Error(err, "Failed to update mirrored object in target cluster", "targetCluster", clusterName)
				}
				continue
			}
		}
	}

	return nil
}

// DeleteMirroredObject deletes a mirrored object from all other clusters
func (m *ResourceMirror) DeleteMirroredObject(ctx context.Context, sourceClusterName string, obj client.Object) error {
	log := m.Log.WithValues(
		"kind", obj.GetObjectKind().GroupVersionKind().Kind,
		"name", obj.GetName(),
		"namespace", obj.GetNamespace(),
		"sourceCluster", sourceClusterName,
	)

	// Get all clusters
	clusters := m.Provider.ListClusters()

	// Delete from each cluster
	for clusterName, targetCluster := range clusters {
		// Skip the source cluster
		if clusterName == sourceClusterName {
			continue
		}

		log.Info("Deleting mirrored object from cluster", "targetCluster", clusterName)

		// Check if object exists in target cluster
		existingObj := obj.DeepCopyObject().(client.Object)
		err := targetCluster.GetClient().Get(ctx, client.ObjectKey{
			Namespace: obj.GetNamespace(),
			Name:      obj.GetName(),
		}, existingObj)

		if err != nil {
			if client.IgnoreNotFound(err) != nil {
				log.Error(err, "Failed to check if object exists in target cluster", "targetCluster", clusterName)
				continue
			}
			// Object doesn't exist, skip
			continue
		}

		// Object exists, delete it
		if err := targetCluster.GetClient().Delete(ctx, existingObj); err != nil {
			log.Error(err, "Failed to delete mirrored object from target cluster", "targetCluster", clusterName)
			continue
		}
	}

	return nil
}

// SetupMirrorWatch sets up a watch for an object type and mirrors changes across clusters
func (m *ResourceMirror) SetupMirrorWatch(ctx context.Context, sourceClusterName string, cl cluster.Cluster, objType client.Object) error {
	log := m.Log.WithValues(
		"kind", objType.GetObjectKind().GroupVersionKind().Kind,
		"sourceCluster", sourceClusterName,
	)

	// Get the informer for the object type
	informer, err := cl.GetCache().GetInformer(ctx, objType)
	if err != nil {
		return fmt.Errorf("failed to get informer: %w", err)
	}

	// Add event handlers
	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if obj, ok := obj.(client.Object); ok {
				if err := m.MirrorObject(ctx, sourceClusterName, obj); err != nil {
					log.Error(err, "Failed to mirror added object")
				}
			}
		},
		UpdateFunc: func(old, new interface{}) {
			if obj, ok := new.(client.Object); ok {
				if err := m.MirrorObject(ctx, sourceClusterName, obj); err != nil {
					log.Error(err, "Failed to mirror updated object")
				}
			}
		},
		DeleteFunc: func(obj interface{}) {
			var clientObj client.Object
			switch t := obj.(type) {
			case client.Object:
				clientObj = t
			case cache.DeletedFinalStateUnknown:
				// Try to get the object from the tombstone
				if obj, ok := t.Obj.(client.Object); ok {
					clientObj = obj
				}
			}
			if clientObj != nil {
				if err := m.DeleteMirroredObject(ctx, sourceClusterName, clientObj); err != nil {
					log.Error(err, "Failed to delete mirrored object")
				}
			}
		},
	})

	return err
}
