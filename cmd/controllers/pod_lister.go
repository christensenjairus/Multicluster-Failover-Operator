/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	kubeconfigprovider "sigs.k8s.io/multicluster-runtime/providers/kubeconfig"
)

// PodWatcher is a simple controller that watches pods across multiple clusters
type PodWatcher struct {
	Manager  mcmanager.Manager
	Provider *kubeconfigprovider.Provider
	Log      logr.Logger
}

// NewPodWatcher creates a new PodWatcher
func NewPodWatcher(mgr mcmanager.Manager, provider multicluster.Provider) *PodWatcher {
	return &PodWatcher{
		Manager:  mgr,
		Provider: provider.(*kubeconfigprovider.Provider),
		Log:      ctrllog.Log.WithName("pod-watcher"),
	}
}

// Start implements Runnable
func (p *PodWatcher) Start(ctx context.Context) error {
	log := p.Log
	log.Info("Starting pod watcher")

	// Get initial list of clusters
	clusters := p.Provider.ListClusters()
	log.Info("Found clusters", "count", len(clusters))

	// Set up watches for each cluster
	for clusterName, cl := range clusters {
		if err := p.setupWatch(ctx, cl, clusterName); err != nil {
			log.Error(err, "Failed to set up watch", "cluster", clusterName)
			// Continue with other clusters
		}
	}

	return nil
}

// Engage implements multicluster.Aware
func (p *PodWatcher) Engage(ctx context.Context, clusterName string, cl cluster.Cluster) error {
	log := p.Log.WithValues("cluster", clusterName)
	log.Info("Setting up watch for new cluster")

	return p.setupWatch(ctx, cl, clusterName)
}

// setupWatch sets up a watch for pods in a cluster
func (p *PodWatcher) setupWatch(ctx context.Context, cl cluster.Cluster, clusterName string) error {
	log := p.Log.WithValues("cluster", clusterName)

	// Initial list of pods
	var pods corev1.PodList
	if err := cl.GetClient().List(ctx, &pods, &client.ListOptions{
		Namespace: "default",
	}); err != nil {
		return err
	}

	log.Info("Initial pod list",
		"count", len(pods.Items))
	for _, pod := range pods.Items {
		log.Info("Pod",
			"name", pod.Name,
			"status", pod.Status.Phase)
	}

	// Get the informer for pods
	informer, err := cl.GetCache().GetInformer(ctx, &corev1.Pod{})
	if err != nil {
		return err
	}

	// Add event handlers
	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			pod := obj.(*corev1.Pod)
			log.Info("Pod added",
				"name", pod.Name,
				"status", pod.Status.Phase)
		},
		UpdateFunc: func(old, new interface{}) {
			oldPod := old.(*corev1.Pod)
			newPod := new.(*corev1.Pod)
			if oldPod.Status.Phase != newPod.Status.Phase {
				log.Info("Pod status changed",
					"name", newPod.Name,
					"oldStatus", oldPod.Status.Phase,
					"newStatus", newPod.Status.Phase)
			}
		},
		DeleteFunc: func(obj interface{}) {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				// If the object is a DeletedFinalStateUnknown, try to get the pod from it
				if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
					pod, ok = tombstone.Obj.(*corev1.Pod)
					if !ok {
						log.Error(nil, "Error decoding pod from tombstone")
						return
					}
				} else {
					log.Error(nil, "Error decoding pod")
					return
				}
			}
			log.Info("Pod deleted", "name", pod.Name)
		},
	})

	return err
}
