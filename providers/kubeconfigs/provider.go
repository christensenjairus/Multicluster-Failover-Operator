/*
Copyright 2025.

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

package kubeconfig

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	toolscache "k8s.io/client-go/tools/cache"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
)

const (
	// DefaultKubeconfigSecretLabel is the default label key to identify kubeconfig secrets
	DefaultKubeconfigSecretLabel = "multicluster.hahomelabs.com/kubeconfig"
	// DefaultKubeconfigSecretKey is the default key in the secret data that contains the kubeconfig
	DefaultKubeconfigSecretKey = "kubeconfig"
)

var _ multicluster.Provider = &Provider{}

// Options are the options for the Kubeconfig Provider.
type Options struct {
	// Namespace to watch for kubeconfig secrets
	Namespace string
	// Label key to identify kubeconfig secrets
	KubeconfigLabel string
	// Key in the secret data that contains the kubeconfig
	KubeconfigKey string
	// ClusterOptions are the options passed to the cluster constructor
	ClusterOptions []cluster.Option
}

// Provider is a cluster provider that watches for secrets containing kubeconfig data
// and engages clusters based on those kubeconfigs.
type Provider struct {
	opts       Options
	log        logr.Logger
	client     client.Client
	lock       sync.RWMutex
	mcMgr      mcmanager.Manager
	clusters   map[string]cluster.Cluster
	cancelFns  map[string]context.CancelFunc
	indexers   []index
}

type index struct {
	object       client.Object
	field        string
	extractValue client.IndexerFunc
}

// New creates a new Kubeconfig Provider.
func New(cl client.Client, opts Options) *Provider {
	// Set defaults
	if opts.KubeconfigLabel == "" {
		opts.KubeconfigLabel = DefaultKubeconfigSecretLabel
	}
	if opts.KubeconfigKey == "" {
		opts.KubeconfigKey = DefaultKubeconfigSecretKey
	}

	return &Provider{
		opts:      opts,
		log:       log.Log.WithName("kubeconfig-provider"),
		client:    cl,
		clusters:  map[string]cluster.Cluster{},
		cancelFns: map[string]context.CancelFunc{},
	}
}

// Run starts the provider and blocks, watching for kubeconfig secrets.
func (p *Provider) Run(ctx context.Context, mgr mcmanager.Manager) error {
	p.log.Info("Starting kubeconfig provider", "namespace", p.opts.Namespace, "label", p.opts.KubeconfigLabel)

	p.lock.Lock()
	p.mcMgr = mgr
	p.lock.Unlock()

	// Setup informer for secrets
	secretInformer, err := p.setupInformer(ctx)
	if err != nil {
		return err
	}

	// Start informer and wait for it
	go secretInformer.Run(ctx.Done())
	if !toolscache.WaitForCacheSync(ctx.Done(), secretInformer.HasSynced) {
		return fmt.Errorf("timed out waiting for caches to sync")
	}

	// Initial list of secrets
	p.initialLoad(ctx)

	// Block until context is done
	<-ctx.Done()
	return nil
}

// Get returns the cluster with the given name, if it is known.
func (p *Provider) Get(_ context.Context, clusterName string) (cluster.Cluster, error) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if cl, ok := p.clusters[clusterName]; ok {
		return cl, nil
	}

	return nil, fmt.Errorf("cluster %s not found", clusterName)
}

// IndexField indexes a field on all clusters, existing and future.
func (p *Provider) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	// Save for future clusters
	p.indexers = append(p.indexers, index{
		object:       obj,
		field:        field,
		extractValue: extractValue,
	})

	// Apply to existing clusters
	for name, cl := range p.clusters {
		if err := cl.GetCache().IndexField(ctx, obj, field, extractValue); err != nil {
			return fmt.Errorf("failed to index field %q on cluster %q: %w", field, name, err)
		}
	}

	return nil
}

// setupInformer sets up an informer for watching secrets
func (p *Provider) setupInformer(ctx context.Context) (toolscache.SharedIndexInformer, error) {
	// Get secret GVK
	secretGVK := corev1.SchemeGroupVersion.WithKind("Secret")

	// Create and setup informer
	informer, err := p.client.Scheme().New(secretGVK)
	if err != nil {
		return nil, fmt.Errorf("failed to create new secret: %w", err)
	}

	secretObj, ok := informer.(*corev1.Secret)
	if !ok {
		return nil, fmt.Errorf("failed to convert to secret")
	}

	// Get informer for secrets
	secretInformer, err := p.client.Watch(ctx, &corev1.SecretList{}, client.InNamespace(p.opts.Namespace))
	if err != nil {
		return nil, fmt.Errorf("failed to create secret informer: %w", err)
	}

	// Register event handlers
	_, err = secretInformer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			secret, ok := obj.(*corev1.Secret)
			if !ok {
				p.log.Error(fmt.Errorf("unexpected type"), "expecting Secret")
				return
			}
			if val, ok := secret.Labels[p.opts.KubeconfigLabel]; !ok || val != "true" {
				return
			}
			p.handleSecretAdd(ctx, secret)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldSecret, ok := oldObj.(*corev1.Secret)
			if !ok {
				p.log.Error(fmt.Errorf("unexpected type"), "expecting Secret")
				return
			}
			newSecret, ok := newObj.(*corev1.Secret)
			if !ok {
				p.log.Error(fmt.Errorf("unexpected type"), "expecting Secret")
				return
			}

			oldHasLabel := false
			if val, ok := oldSecret.Labels[p.opts.KubeconfigLabel]; ok && val == "true" {
				oldHasLabel = true
			}

			newHasLabel := false
			if val, ok := newSecret.Labels[p.opts.KubeconfigLabel]; ok && val == "true" {
				newHasLabel = true
			}

			// If label was added
			if !oldHasLabel && newHasLabel {
				p.handleSecretAdd(ctx, newSecret)
				return
			}

			// If label was removed
			if oldHasLabel && !newHasLabel {
				p.handleSecretDelete(ctx, oldSecret)
				return
			}

			// If label still exists and content has changed, update the cluster
			if newHasLabel && oldSecret.ResourceVersion != newSecret.ResourceVersion {
				p.handleSecretUpdate(ctx, oldSecret, newSecret)
			}
		},
		DeleteFunc: func(obj interface{}) {
			secret, ok := obj.(*corev1.Secret)
			if !ok {
				// In case of a delete, the object could be a DeletedFinalStateUnknown
				if tombstone, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
					secret, ok = tombstone.Obj.(*corev1.Secret)
					if !ok {
						p.log.Error(fmt.Errorf("unexpected type"), "expecting Secret")
						return
					}
				} else {
					p.log.Error(fmt.Errorf("unexpected type"), "expecting Secret")
					return
				}
			}
			if val, ok := secret.Labels[p.opts.KubeconfigLabel]; !ok || val != "true" {
				return
			}
			p.handleSecretDelete(ctx, secret)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to add event handler: %w", err)
	}

	return secretInformer, nil
}

// initialLoad lists and processes all existing secrets with the kubeconfig label
func (p *Provider) initialLoad(ctx context.Context) {
	secretList := &corev1.SecretList{}
	if err := p.client.List(ctx, secretList, client.InNamespace(p.opts.Namespace), client.MatchingLabels{p.opts.KubeconfigLabel: "true"}); err != nil {
		p.log.Error(err, "failed to list secrets")
		return
	}

	for i := range secretList.Items {
		p.handleSecretAdd(ctx, &secretList.Items[i])
	}
}

// handleSecretAdd handles the addition of a new kubeconfig secret
func (p *Provider) handleSecretAdd(ctx context.Context, secret *corev1.Secret) {
	log := p.log.WithValues("secret", types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace})
	log.Info("Processing kubeconfig secret")

	clusterName := secret.Name

	// Check if we already have this cluster
	p.lock.RLock()
	_, exists := p.clusters[clusterName]
	p.lock.RUnlock()

	if exists {
		log.Info("Cluster already engaged")
		return
	}

	// Get kubeconfig from secret
	kubeconfigData, ok := secret.Data[p.opts.KubeconfigKey]
	if !ok || len(kubeconfigData) == 0 {
		log.Error(nil, "kubeconfig key not found or empty", "key", p.opts.KubeconfigKey)
		return
	}

	// Parse kubeconfig and create REST config
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
	if err != nil {
		log.Error(err, "failed to parse kubeconfig")
		return
	}

	// Create and start cluster
	cl, err := p.createAndStartCluster(ctx, clusterName, restConfig, log)
	if err != nil {
		log.Error(err, "failed to create/start cluster")
		return
	}

	// Engage with manager
	if err := p.engageCluster(ctx, clusterName, cl); err != nil {
		log.Error(err, "failed to engage cluster with manager")
		return
	}

	log.Info("Successfully engaged cluster")
}

// handleSecretUpdate handles updates to a kubeconfig secret
func (p *Provider) handleSecretUpdate(ctx context.Context, oldSecret, newSecret *corev1.Secret) {
	log := p.log.WithValues("secret", types.NamespacedName{Name: newSecret.Name, Namespace: newSecret.Namespace})
	log.Info("Handling kubeconfig secret update")

	clusterName := newSecret.Name

	// We need to stop the old cluster, then create a new one
	p.handleSecretDelete(ctx, oldSecret)
	p.handleSecretAdd(ctx, newSecret)
}

// handleSecretDelete handles the deletion of a kubeconfig secret
func (p *Provider) handleSecretDelete(ctx context.Context, secret *corev1.Secret) {
	log := p.log.WithValues("secret", types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace})
	log.Info("Handling kubeconfig secret deletion")

	clusterName := secret.Name

	p.lock.Lock()
	defer p.lock.Unlock()

	// Find and stop the cluster if it exists
	cancelFn, clusterExists := p.cancelFns[clusterName]
	if !clusterExists {
		return
	}

	// Cancel the context and cleanup
	cancelFn()
	delete(p.clusters, clusterName)
	delete(p.cancelFns, clusterName)

	log.Info("Disengaged cluster")
}

// createAndStartCluster creates a new controller-runtime cluster and starts it
func (p *Provider) createAndStartCluster(ctx context.Context, clusterName string, config *rest.Config, log logr.Logger) (cluster.Cluster, error) {
	// Create cluster
	cl, err := cluster.New(config, p.opts.ClusterOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster: %w", err)
	}

	// Apply indexers to new cluster
	p.lock.RLock()
	indexers := make([]index, len(p.indexers))
	copy(indexers, p.indexers)
	p.lock.RUnlock()

	for _, idx := range indexers {
		if err := cl.GetCache().IndexField(ctx, idx.object, idx.field, idx.extractValue); err != nil {
			return nil, fmt.Errorf("failed to index field %q: %w", idx.field, err)
		}
	}

	// Create new context for this cluster
	clusterCtx, cancel := context.WithCancel(ctx)

	// Start the cluster
	go func() {
		if err := cl.Start(clusterCtx); err != nil {
			log.Error(err, "failed to start cluster")
			return
		}
	}()

	// Wait for cache sync
	if !cl.GetCache().WaitForCacheSync(clusterCtx) {
		cancel()
		return nil, fmt.Errorf("failed to sync cache")
	}

	// Add to our maps
	p.lock.Lock()
	p.clusters[clusterName] = cl
	p.cancelFns[clusterName] = cancel
	p.lock.Unlock()

	return cl, nil
}

// engageCluster engages a cluster with the multicluster manager
func (p *Provider) engageCluster(ctx context.Context, clusterName string, cl cluster.Cluster) error {
	p.lock.RLock()
	mgr := p.mcMgr
	p.lock.RUnlock()

	// If the provider hasn't been started yet
	if mgr == nil {
		return fmt.Errorf("provider not initialized with manager")
	}

	// Engage the manager with this cluster
	if err := mgr.Engage(ctx, clusterName, cl); err != nil {
		// Clean up on error
		p.lock.Lock()
		if cancelFn, exists := p.cancelFns[clusterName]; exists {
			cancelFn()
		}
		delete(p.clusters, clusterName)
		delete(p.cancelFns, clusterName)
		p.lock.Unlock()

		return fmt.Errorf("failed to engage manager: %w", err)
	}

	return nil
}