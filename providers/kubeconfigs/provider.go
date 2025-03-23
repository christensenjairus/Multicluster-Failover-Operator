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

package kubeconfigs

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// DefaultKubeconfigSecretLabel is the default label key to identify kubeconfig secrets
	DefaultKubeconfigSecretLabel = "sigs.k8s.io/multicluster-runtime-kubeconfig"
	// DefaultKubeconfigSecretKey is the default key in the secret data that contains the kubeconfig
	DefaultKubeconfigSecretKey = "kubeconfig"
	// PollInterval is how often to poll for secrets
	PollInterval = 30 * time.Second
)

// Manager represents a multicluster manager interface
type Manager interface {
	// Engage engages a cluster with the manager
	Engage(ctx context.Context, name string, cl cluster.Cluster) error
	// Get returns a cluster with the given name
	Get(ctx context.Context, name string) (cluster.Cluster, error)
}

// Provider interface for cluster discovery and management
type Provider interface {
	// Get returns a cluster with the given name
	Get(ctx context.Context, name string) (cluster.Cluster, error)
	// Run starts the provider
	Run(ctx context.Context, mgr Manager) error
	// IndexField registers a field indexer for all clusters
	IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error
}

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

// KubeconfigProvider is a cluster provider that watches for secrets containing kubeconfig data
// and engages clusters based on those kubeconfigs.
type KubeconfigProvider struct {
	opts       Options
	log        logr.Logger
	client     client.Client
	lock       sync.RWMutex
	mcMgr      Manager
	clusters   map[string]cluster.Cluster
	cancelFns  map[string]context.CancelFunc
	indexers   []index
	seenHashes map[string]string // tracks resource versions
}

type index struct {
	object       client.Object
	field        string
	extractValue client.IndexerFunc
}

// New creates a new Kubeconfig Provider.
func New(cl client.Client, opts Options) *KubeconfigProvider {
	// Set defaults
	if opts.KubeconfigLabel == "" {
		opts.KubeconfigLabel = DefaultKubeconfigSecretLabel
	}
	if opts.KubeconfigKey == "" {
		opts.KubeconfigKey = DefaultKubeconfigSecretKey
	}

	return &KubeconfigProvider{
		opts:       opts,
		log:        log.Log.WithName("kubeconfig-provider"),
		client:     cl,
		clusters:   map[string]cluster.Cluster{},
		cancelFns:  map[string]context.CancelFunc{},
		seenHashes: map[string]string{},
	}
}

// Run starts the provider and blocks, watching for kubeconfig secrets.
func (p *KubeconfigProvider) Run(ctx context.Context, mgr Manager) error {
	p.log.Info("Starting kubeconfig provider", "namespace", p.opts.Namespace, "label", p.opts.KubeconfigLabel)

	p.lock.Lock()
	p.mcMgr = mgr
	p.lock.Unlock()

	// Initial list of secrets
	if err := p.syncSecrets(ctx); err != nil {
		p.log.Error(err, "initial secret sync failed")
	}

	// Log the managed clusters after initial sync
	p.logManagedClusters()

	// Create a Kubernetes clientset for watching
	var config *rest.Config
	var err error

	// First, try to get the config from controller-runtime
	config, err = rest.InClusterConfig()
	if err != nil {
		p.log.Info("Not running in-cluster, using kubeconfig for local development")

		// Look for kubeconfig in default locations
		rules := clientcmd.NewDefaultClientConfigLoadingRules()
		rules.DefaultClientConfig = &clientcmd.DefaultClientConfig

		clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{})
		config, err = clientConfig.ClientConfig()
		if err != nil {
			return fmt.Errorf("failed to create config: %w", err)
		}
	}

	p.log.Info("Successfully connected to Kubernetes API", "host", config.Host)

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	// Set up label selector for our kubeconfig label
	labelSelector := fmt.Sprintf("%s=true", p.opts.KubeconfigLabel)
	p.log.Info("Watching for kubeconfig secrets", "selector", labelSelector)

	// Watch for secret changes
	go func() {
		for {
			// Create a watch for secrets
			watcher, err := clientset.CoreV1().Secrets(p.opts.Namespace).Watch(ctx, metav1.ListOptions{
				LabelSelector: labelSelector,
			})
			if err != nil {
				p.log.Error(err, "failed to create watch, retrying")
				time.Sleep(5 * time.Second)
				continue
			}

			// Process watch events
			for event := range watcher.ResultChan() {
				secret, ok := event.Object.(*corev1.Secret)
				if !ok {
					p.log.Error(fmt.Errorf("unexpected object type"), "expected Secret",
						"type", fmt.Sprintf("%T", event.Object))
					continue
				}

				p.log.Info("Received secret event", "type", event.Type, "name", secret.Name)

				switch event.Type {
				case watch.Added, watch.Modified:
					p.handleSecretUpsert(ctx, secret)
				case watch.Deleted:
					p.handleSecretDelete(ctx, secret)
				}

				// Log managed clusters after each change
				p.logManagedClusters()
			}

			// If we get here, the watch channel was closed, so we'll retry
			p.log.Info("Watch channel closed, retrying")
			time.Sleep(2 * time.Second)
		}
	}()

	// Sync secrets periodically as a backup mechanism
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := p.syncSecrets(ctx); err != nil {
				p.log.Error(err, "failed to sync secrets")
			}
		}
	}
}

// hasKubeconfigLabel checks if a secret has our kubeconfig label
func (p *KubeconfigProvider) hasKubeconfigLabel(secret *corev1.Secret) bool {
	value, exists := secret.Labels[p.opts.KubeconfigLabel]
	return exists && value == "true"
}

// secretReconciler implements controller.Reconciler to reconcile Secret objects
type secretReconciler struct {
	provider *KubeconfigProvider
	log      logr.Logger
}

// Reconcile handles Secret events
func (r *secretReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithValues("secret", req.NamespacedName)

	// Get the secret
	secret := &corev1.Secret{}
	err := r.provider.client.Get(ctx, req.NamespacedName, secret)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			log.Error(err, "Failed to get Secret")
			return reconcile.Result{}, err
		}
		// Secret was deleted
		log.Info("Secret deleted, disengaging cluster")
		r.provider.handleSecretDelete(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      req.Name,
				Namespace: req.Namespace,
			},
		})
		return reconcile.Result{}, nil
	}

	// Handle create/update
	log.Info("Secret created or updated")
	r.provider.handleSecretUpsert(ctx, secret)

	// Log managed clusters after changes
	r.provider.logManagedClusters()

	return reconcile.Result{}, nil
}

// logManagedClusters logs the list of all currently managed clusters
func (p *KubeconfigProvider) logManagedClusters() {
	p.lock.RLock()
	defer p.lock.RUnlock()

	if len(p.clusters) == 0 {
		p.log.Info("No clusters are currently being managed")
		return
	}

	// Get a sorted list of cluster names
	clusterNames := make([]string, 0, len(p.clusters))
	for name := range p.clusters {
		clusterNames = append(clusterNames, name)
	}

	p.log.Info("Currently managing the following clusters", "clusters", strings.Join(clusterNames, ", "), "count", len(clusterNames))
}

// syncSecrets lists all matching secrets and processes them
func (p *KubeconfigProvider) syncSecrets(ctx context.Context) error {
	secretList := &corev1.SecretList{}
	if err := p.client.List(ctx, secretList, client.InNamespace(p.opts.Namespace), client.MatchingLabels{p.opts.KubeconfigLabel: "true"}); err != nil {
		return fmt.Errorf("failed to list secrets: %w", err)
	}

	// Process existing secrets
	currentKeys := make(map[string]bool)
	for i := range secretList.Items {
		secret := &secretList.Items[i]
		key := fmt.Sprintf("%s/%s", secret.Namespace, secret.Name)
		currentKeys[key] = true

		// Check if this is a new or updated secret
		if hash, exists := p.seenHashes[key]; !exists || hash != secret.ResourceVersion {
			p.handleSecretUpsert(ctx, secret)
			p.seenHashes[key] = secret.ResourceVersion
		}
	}

	// Check for deleted secrets
	p.lock.RLock()
	for name := range p.clusters {
		key := fmt.Sprintf("%s/%s", p.opts.Namespace, name)
		if _, exists := currentKeys[key]; !exists {
			// This secret has been deleted
			p.lock.RUnlock()
			p.handleSecretDelete(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: p.opts.Namespace,
				},
			})
			p.lock.RLock()
			// Remove from seen hashes
			delete(p.seenHashes, key)
		}
	}
	p.lock.RUnlock()

	return nil
}

// Get returns the cluster with the given name, if it is known.
func (p *KubeconfigProvider) Get(_ context.Context, clusterName string) (cluster.Cluster, error) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if cl, ok := p.clusters[clusterName]; ok {
		return cl, nil
	}

	return nil, fmt.Errorf("cluster %s not found", clusterName)
}

// IndexField indexes a field on all clusters, existing and future.
func (p *KubeconfigProvider) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
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

// handleSecretUpsert handles the addition or update of a kubeconfig secret
func (p *KubeconfigProvider) handleSecretUpsert(ctx context.Context, secret *corev1.Secret) {
	log := p.log.WithValues("secret", types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace})
	log.Info("Processing kubeconfig secret")

	clusterName := secret.Name

	// Check if we already have this cluster
	p.lock.RLock()
	_, exists := p.clusters[clusterName]
	existingCancelFn := p.cancelFns[clusterName]
	p.lock.RUnlock()

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

	// If cluster exists and it's an update, we need to stop the old one and create a new one
	if exists {
		log.Info("Updating existing cluster")
		if existingCancelFn != nil {
			existingCancelFn()
		}
		p.lock.Lock()
		delete(p.clusters, clusterName)
		delete(p.cancelFns, clusterName)
		p.lock.Unlock()
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

// handleSecretDelete handles the deletion of a kubeconfig secret
func (p *KubeconfigProvider) handleSecretDelete(ctx context.Context, secret *corev1.Secret) {
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
func (p *KubeconfigProvider) createAndStartCluster(ctx context.Context, clusterName string, config *rest.Config, log logr.Logger) (cluster.Cluster, error) {
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
func (p *KubeconfigProvider) engageCluster(ctx context.Context, clusterName string, cl cluster.Cluster) error {
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
