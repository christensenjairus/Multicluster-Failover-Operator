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

// Package kubeconfig provides a Kubernetes cluster provider that watches secrets
// containing kubeconfig data and creates controller-runtime clusters for each.
package kubeconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
)

const (
	// DefaultKubeconfigSecretLabel is the default label key to identify kubeconfig secrets
	DefaultKubeconfigSecretLabel = "sigs.k8s.io/multicluster-runtime-kubeconfig"

	// DefaultKubeconfigSecretKey is the default key in the secret data that contains the kubeconfig
	DefaultKubeconfigSecretKey = "kubeconfig"
)

var _ multicluster.Provider = &Provider{}

// New creates a new Kubeconfig Provider.
func New(opts Options) *Provider {
	// Set defaults
	if opts.KubeconfigLabel == "" {
		opts.KubeconfigLabel = DefaultKubeconfigSecretLabel
	}
	if opts.KubeconfigKey == "" {
		opts.KubeconfigKey = DefaultKubeconfigSecretKey
	}
	if opts.ConnectionTimeout == 0 {
		opts.ConnectionTimeout = 10 * time.Second
	}
	if opts.CacheSyncTimeout == 0 {
		opts.CacheSyncTimeout = 30 * time.Second
	}
	if opts.KubeconfigPath == "" {
		opts.KubeconfigPath = filepath.Join(os.Getenv("HOME"), ".kube", "config")
	}

	return &Provider{
		opts:        opts,
		log:         log.Log.WithName("kubeconfig-provider"),
		client:      nil, // Will be set in Run
		clusters:    map[string]cluster.Cluster{},
		cancelFns:   map[string]context.CancelFunc{},
		seenHashes:  map[string]string{},
		readySignal: make(chan struct{}),
	}
}

// Options are the options for the Kubeconfig Provider.
type Options struct {
	// Namespace to watch for kubeconfig secrets
	Namespace string

	// Label key to identify kubeconfig secrets
	KubeconfigLabel string

	// Key in the secret data that contains the kubeconfig
	KubeconfigKey string

	// ConnectionTimeout is the timeout for connecting to a cluster
	ConnectionTimeout time.Duration

	// CacheSyncTimeout is the timeout for waiting for the cache to sync
	CacheSyncTimeout time.Duration

	// KubeconfigPath is the path to the kubeconfig file to use for development/testing
	// If not set, will use the default ~/.kube/config
	KubeconfigPath string
}

type index struct {
	object       client.Object
	field        string
	extractValue client.IndexerFunc
}

// Provider is a cluster provider that watches for secrets containing kubeconfig data
// and engages clusters based on those kubeconfigs.
type Provider struct {
	opts        Options
	log         logr.Logger
	client      client.Client
	lock        sync.RWMutex
	clusters    map[string]cluster.Cluster
	cancelFns   map[string]context.CancelFunc
	indexers    []index
	seenHashes  map[string]string // tracks resource versions
	readySignal chan struct{}     // Signal when provider is ready to start
	readyOnce   sync.Once         // Ensure we only signal once
}

// IsReady returns a channel that will be closed when the provider is ready to start
func (p *Provider) IsReady() <-chan struct{} {
	return p.readySignal
}

// Get returns the cluster with the given name, if it is known.
func (p *Provider) Get(ctx context.Context, clusterName string) (cluster.Cluster, error) {
	p.lock.RLock()
	defer p.lock.RUnlock()

	if cl, ok := p.clusters[clusterName]; ok {
		return cl, nil
	}

	return nil, fmt.Errorf("cluster %s not found", clusterName)
}

// Run starts the provider and blocks, watching for kubeconfig secrets.
func (p *Provider) Run(ctx context.Context, mgr mcmanager.Manager) error {
	p.log.Info("Starting kubeconfig provider", "namespace", p.opts.Namespace, "label", p.opts.KubeconfigLabel)

	// If client isn't set yet, get it from the manager
	if p.client == nil && mgr != nil {
		p.log.Info("Setting client from manager")
		p.client = mgr.GetLocalManager().GetClient()
		if p.client == nil {
			return fmt.Errorf("failed to get client from manager")
		}
	}

	// Get the informer for secrets
	secretInf, err := mgr.GetLocalManager().GetCache().GetInformer(ctx, &corev1.Secret{})
	if err != nil {
		return fmt.Errorf("failed to get secret informer: %w", err)
	}

	// Add event handlers for secrets
	if _, err := secretInf.AddEventHandler(toolscache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			secret, ok := obj.(*corev1.Secret)
			if !ok {
				return false
			}
			// Only process secrets in our namespace with our label
			return secret.Namespace == p.opts.Namespace &&
				secret.Labels[p.opts.KubeconfigLabel] == "true"
		},
		Handler: toolscache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				secret := obj.(*corev1.Secret)
				p.log.Info("Processing new secret", "name", secret.Name)
				if err := p.handleSecret(ctx, secret, mgr); err != nil {
					p.log.Error(err, "Failed to handle secret", "name", secret.Name)
				}
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				secret := newObj.(*corev1.Secret)
				p.log.Info("Processing updated secret", "name", secret.Name)
				if err := p.handleSecret(ctx, secret, mgr); err != nil {
					p.log.Error(err, "Failed to handle secret", "name", secret.Name)
				}
			},
			DeleteFunc: func(obj interface{}) {
				secret := obj.(*corev1.Secret)
				p.log.Info("Processing deleted secret", "name", secret.Name)
				p.handleSecretDelete(secret)
			},
		},
	}); err != nil {
		return fmt.Errorf("failed to add event handlers: %w", err)
	}

	// Signal readiness after setting up informer
	p.readyOnce.Do(func() {
		p.log.Info("Signaling that KubeconfigProvider is ready to start")
		close(p.readySignal)
	})

	// Block until context is done
	<-ctx.Done()
	p.log.Info("Context cancelled, exiting provider")
	return ctx.Err()
}

// handleSecret processes a secret containing kubeconfig data
func (p *Provider) handleSecret(ctx context.Context, secret *corev1.Secret, mgr mcmanager.Manager) error {
	if secret == nil {
		return fmt.Errorf("received nil secret")
	}

	// Extract name to use as cluster name
	clusterName := secret.Name
	log := p.log.WithValues("cluster", clusterName, "secret", fmt.Sprintf("%s/%s", secret.Namespace, secret.Name))

	// Check if this secret has kubeconfig data
	kubeconfigData, ok := secret.Data[p.opts.KubeconfigKey]
	if !ok {
		log.Info("Secret does not contain kubeconfig data", "key", p.opts.KubeconfigKey)
		return nil
	}

	// Hash the kubeconfig to detect changes
	dataHash := hashBytes(kubeconfigData)

	// Check if we've seen this version before
	p.lock.RLock()
	existingHash, exists := p.seenHashes[clusterName]
	p.lock.RUnlock()

	if exists && existingHash == dataHash {
		log.Info("Kubeconfig unchanged, skipping")
		return nil
	}

	// Parse the kubeconfig
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
	if err != nil {
		return fmt.Errorf("failed to parse kubeconfig: %w", err)
	}

	// Set reasonable defaults for the client
	restConfig.Timeout = p.opts.ConnectionTimeout

	// Check if we already have this cluster
	p.lock.RLock()
	_, clusterExists := p.clusters[clusterName]
	p.lock.RUnlock()

	// If the cluster already exists, remove it first
	if clusterExists {
		log.Info("Cluster already exists, updating it")
		if err := p.removeCluster(clusterName); err != nil {
			return fmt.Errorf("failed to remove existing cluster: %w", err)
		}
	}

	// Create a new cluster
	log.Info("Creating new cluster from kubeconfig")
	cl, err := cluster.New(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create cluster: %w", err)
	}

	// Apply any field indexers
	for _, idx := range p.indexers {
		if err := cl.GetFieldIndexer().IndexField(ctx, idx.object, idx.field, idx.extractValue); err != nil {
			return fmt.Errorf("failed to index field %q: %w", idx.field, err)
		}
	}

	// Create a context that will be canceled when this cluster is removed
	clusterCtx, cancel := context.WithCancel(ctx)

	// Start the cluster
	go func() {
		if err := cl.Start(clusterCtx); err != nil {
			log.Error(err, "Failed to start cluster")
		}
	}()

	// Wait for cache to sync
	log.Info("Waiting for cluster cache to sync", "timeout", p.opts.CacheSyncTimeout)
	syncCtx, syncCancel := context.WithTimeout(ctx, p.opts.CacheSyncTimeout)
	defer syncCancel()

	if !cl.GetCache().WaitForCacheSync(syncCtx) {
		cancel() // Cancel the cluster context
		return fmt.Errorf("timeout waiting for cache to sync")
	}

	// Store the cluster
	p.lock.Lock()
	p.clusters[clusterName] = cl
	p.cancelFns[clusterName] = cancel
	p.seenHashes[clusterName] = dataHash
	p.lock.Unlock()

	log.Info("Successfully added cluster")

	// Engage the manager if provided
	if mgr != nil {
		if err := mgr.Engage(clusterCtx, clusterName, cl); err != nil {
			log.Error(err, "Failed to engage manager, removing cluster")
			p.lock.Lock()
			delete(p.clusters, clusterName)
			delete(p.cancelFns, clusterName)
			delete(p.seenHashes, clusterName)
			p.lock.Unlock()
			cancel() // Cancel the cluster context
			return fmt.Errorf("failed to engage manager: %w", err)
		}
		log.Info("Successfully engaged manager")
	}

	return nil
}

// handleSecretDelete handles the deletion of a secret
func (p *Provider) handleSecretDelete(secret *corev1.Secret) {
	if secret == nil {
		return
	}

	clusterName := secret.Name
	log := p.log.WithValues("cluster", clusterName)

	log.Info("Handling deleted secret")

	// Remove the cluster
	if err := p.removeCluster(clusterName); err != nil {
		log.Error(err, "Failed to remove cluster")
	}
}

// removeCluster removes a cluster by name
func (p *Provider) removeCluster(clusterName string) error {
	log := p.log.WithValues("cluster", clusterName)
	log.Info("Removing cluster")

	// Find the cluster and cancel function
	p.lock.RLock()
	_, exists := p.clusters[clusterName]
	if !exists {
		p.lock.RUnlock()
		return fmt.Errorf("cluster %s not found", clusterName)
	}

	// Get the cancel function
	cancelFn, exists := p.cancelFns[clusterName]
	if !exists {
		p.lock.RUnlock()
		return fmt.Errorf("cancel function for cluster %s not found", clusterName)
	}
	p.lock.RUnlock()

	// Cancel the context to trigger cleanup for this cluster
	cancelFn()
	log.Info("Cancelled cluster context")

	// Clean up our maps
	p.lock.Lock()
	delete(p.clusters, clusterName)
	delete(p.cancelFns, clusterName)
	delete(p.seenHashes, clusterName)
	p.lock.Unlock()

	log.Info("Successfully removed cluster")
	return nil
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
		if err := cl.GetFieldIndexer().IndexField(ctx, obj, field, extractValue); err != nil {
			return fmt.Errorf("failed to index field %q on cluster %q: %w", field, name, err)
		}
	}

	return nil
}

// ListClusters returns a list of all discovered clusters.
func (p *Provider) ListClusters() map[string]cluster.Cluster {
	p.lock.RLock()
	defer p.lock.RUnlock()

	// Return a copy of the map to avoid race conditions
	result := make(map[string]cluster.Cluster, len(p.clusters))
	for k, v := range p.clusters {
		result[k] = v
	}
	return result
}

// hashBytes returns a hex-encoded SHA256 hash of the given bytes
func hashBytes(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}
