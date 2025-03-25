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

// Package kubeconfig provides a Kubernetes cluster provider that watches secrets
// containing kubeconfig data and creates controller-runtime clusters for each.
package kubeconfig

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/christensenjairus/Multicluster-Failover-Operator/internal/manager"
	"github.com/multicluster-runtime/multicluster-runtime/pkg/multicluster"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// DefaultKubeconfigSecretLabel is the default label key to identify kubeconfig secrets
	DefaultKubeconfigSecretLabel = "sigs.k8s.io/multicluster-runtime-kubeconfig"

	// DefaultKubeconfigSecretKey is the default key in the secret data that contains the kubeconfig
	DefaultKubeconfigSecretKey = "kubeconfig"
)

// index defines a field indexer
type index struct {
	object       client.Object
	field        string
	extractValue client.IndexerFunc
}

// Options are the options for the Kubeconfig Provider.
type Options struct {
	// Namespace to watch for kubeconfig secrets
	Namespace string

	// Label key to identify kubeconfig secrets
	KubeconfigLabel string

	// Key in the secret data that contains the kubeconfig
	KubeconfigKey string

	// Scheme is the scheme to use for the cluster. If not provided, a new one will be created.
	Scheme *runtime.Scheme

	// ConnectionTimeout is the timeout for connecting to a cluster
	ConnectionTimeout time.Duration

	// CacheSyncTimeout is the timeout for waiting for the cache to sync
	CacheSyncTimeout time.Duration
}

// KubeconfigProvider is a cluster provider that watches for secrets containing kubeconfig data
// and engages clusters based on those kubeconfig.
type KubeconfigProvider struct {
	opts       Options
	log        logr.Logger
	client     client.Client
	Client     client.Client // For controller-runtime Reconciler interface
	lock       sync.RWMutex
	manager    manager.ClusterManager
	clusters   map[string]cluster.Cluster
	cancelFns  map[string]context.CancelFunc
	indexers   []index
	seenHashes map[string]string // tracks resource versions
}

// Ensure KubeconfigProvider implements the multicluster.Provider interface
var _ multicluster.Provider = &KubeconfigProvider{}

// New creates a new Kubeconfig Provider.
func New(cl client.Client, opts Options) *KubeconfigProvider {
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

	return &KubeconfigProvider{
		opts:       opts,
		log:        log.Log.WithName("kubeconfig-provider"),
		client:     cl,
		Client:     cl, // Set both client fields
		clusters:   map[string]cluster.Cluster{},
		cancelFns:  map[string]context.CancelFunc{},
		seenHashes: map[string]string{},
	}
}

// Get returns the cluster with the given name, if it is known.
// It implements the multicluster.Provider interface.
func (p *KubeconfigProvider) Get(_ context.Context, clusterName string) (cluster.Cluster, error) {
	p.lock.RLock()
	defer p.lock.RUnlock()

	if cl, ok := p.clusters[clusterName]; ok {
		return cl, nil
	}

	return nil, fmt.Errorf("cluster %s not found", clusterName)
}

// Run starts the provider and blocks, watching for kubeconfig secrets.
// It implements the multicluster.Provider interface.
func (p *KubeconfigProvider) Run(ctx context.Context, mgr manager.ClusterManager) error {
	p.log.Info("Starting kubeconfig provider", "namespace", p.opts.Namespace, "label", p.opts.KubeconfigLabel)

	p.lock.Lock()
	p.manager = mgr
	p.lock.Unlock()

	// Initial list of secrets
	if err := p.syncSecrets(ctx); err != nil {
		p.log.Error(err, "initial secret sync failed")
	}

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
	go p.watchSecrets(ctx, clientset, labelSelector)

	return nil
}

// Reconcile implements the controller-runtime Reconciler interface
func (p *KubeconfigProvider) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := p.log.WithValues("secret", req.NamespacedName)
	secret := &corev1.Secret{}

	if err := p.Client.Get(ctx, req.NamespacedName, secret); err != nil {
		if errors.IsNotFound(err) {
			log.Info("Secret not found, handling deletion")
			// Secret was deleted, handle deletion
			p.handleSecretDelete(&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      req.Name,
					Namespace: req.Namespace,
				},
			})
			return reconcile.Result{}, nil
		}
		log.Error(err, "Failed to get secret")
		return reconcile.Result{}, fmt.Errorf("failed to get secret: %w", err)
	}

	log.Info("Processing secret")
	// Handle secret update/creation
	p.handleSecretUpsert(ctx, secret)

	return reconcile.Result{}, nil
}

// IndexField indexes a field on all clusters, existing and future.
// It implements the multicluster.Provider interface.
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

// Engage creates, starts and registers a new cluster with the manager
func (p *KubeconfigProvider) Engage(ctx context.Context, clusterName string, config *rest.Config) error {
	log := p.log.WithValues("cluster", clusterName)
	log.Info("Creating new controller-runtime cluster")

	// Add timeout to the config
	config.Timeout = p.opts.ConnectionTimeout

	// Use provided scheme or create a new one
	s := p.opts.Scheme
	if s == nil {
		s = runtime.NewScheme()
		_ = corev1.AddToScheme(s) // Add core types
	}

	// Create options with our scheme
	options := cluster.Options{
		Scheme: s,
	}

	// Create the cluster with our scheme
	cl, err := cluster.New(config, func(o *cluster.Options) {
		*o = options
	})
	if err != nil {
		return fmt.Errorf("failed to create cluster: %w", err)
	}

	// Apply indexers to new cluster
	p.lock.RLock()
	indexers := make([]index, len(p.indexers))
	copy(indexers, p.indexers)
	p.lock.RUnlock()

	for _, idx := range indexers {
		if err := cl.GetCache().IndexField(ctx, idx.object, idx.field, idx.extractValue); err != nil {
			return fmt.Errorf("failed to index field %q: %w", idx.field, err)
		}
	}

	// Create new context for this cluster
	clusterCtx, cancel := context.WithCancel(ctx)

	// Start the cluster
	log.Info("Starting cluster client")
	go func() {
		if err := cl.Start(clusterCtx); err != nil {
			log.Error(err, "failed to start cluster")
			return
		}
	}()

	// Wait for cache sync with a timeout
	syncCtx, cancelSync := context.WithTimeout(ctx, p.opts.CacheSyncTimeout)
	defer cancelSync()

	log.Info("Waiting for cache sync")
	if !cl.GetCache().WaitForCacheSync(syncCtx) {
		cancel()
		return fmt.Errorf("failed to sync cache within timeout")
	}
	log.Info("Cache sync completed successfully")

	// Test that the client works
	testPods := &corev1.PodList{}
	if err := cl.GetClient().List(ctx, testPods, client.InNamespace("default"), client.Limit(1)); err != nil {
		log.Error(err, "cluster client test failed - couldn't list pods")
		cancel()
		return fmt.Errorf("cluster client test failed: %w", err)
	}
	log.Info("Cluster client test successful", "podsFound", len(testPods.Items))

	// Add to our maps
	p.lock.Lock()
	p.clusters[clusterName] = cl
	p.cancelFns[clusterName] = cancel
	p.lock.Unlock()

	// Engage with manager
	p.lock.RLock()
	mgr := p.manager
	p.lock.RUnlock()

	// If the provider hasn't been started yet
	if mgr == nil {
		cancel()
		return fmt.Errorf("provider not initialized with manager")
	}

	log.Info("Engaging cluster with manager")
	if err := mgr.Engage(ctx, clusterName, cl); err != nil {
		cancel()
		p.lock.Lock()
		delete(p.clusters, clusterName)
		delete(p.cancelFns, clusterName)
		p.lock.Unlock()
		return fmt.Errorf("failed to engage manager: %w", err)
	}

	log.Info("Successfully engaged cluster")
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

	log.Info("Found kubeconfig data in secret", "dataSize", len(kubeconfigData))

	// Parse kubeconfig and create REST config
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
	if err != nil {
		log.Error(err, "failed to parse kubeconfig")
		return
	}

	// Test connection to API server
	log.Info("Testing connection to API server", "host", restConfig.Host)
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		log.Error(err, "failed to create clientset from config")
		return
	}

	// Attempt to list nodes as a basic connectivity test
	_, err = clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		log.Error(err, "failed to connect to Kubernetes API server", "host", restConfig.Host)
		return
	}
	log.Info("Successfully connected to API server", "host", restConfig.Host)

	// If cluster exists and it's an update, we need to stop the old one
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
	if err := p.Engage(ctx, clusterName, restConfig); err != nil {
		log.Error(err, "failed to engage cluster")
		return
	}

	// Log current cluster count
	p.lock.RLock()
	clusterCount := len(p.clusters)
	p.lock.RUnlock()
	log.Info("Currently managing clusters", "count", clusterCount)
}

// Disengage stops and removes a cluster from the provider
func (p *KubeconfigProvider) Disengage(ctx context.Context, clusterName string) error {
	log := p.log.WithValues("cluster", clusterName)
	log.Info("Disengaging cluster")

	p.lock.Lock()
	defer p.lock.Unlock()

	// Find the cluster and cancel function
	_, exists := p.clusters[clusterName]
	if !exists {
		return fmt.Errorf("cluster %s not found", clusterName)
	}

	// Get the cancel function
	cancelFn, exists := p.cancelFns[clusterName]
	if !exists {
		return fmt.Errorf("cancel function for cluster %s not found", clusterName)
	}

	// Disengage from manager first
	p.lock.RUnlock() // Temporarily unlock for manager call
	p.lock.Lock()
	mgr := p.manager
	if mgr != nil {
		if err := mgr.Disengage(ctx, clusterName); err != nil {
			log.Error(err, "failed to disengage from manager")
			// Continue with cleanup even if manager disengage fails
		}
	}
	p.lock.Lock() // Lock again for cleanup

	// Stop the cluster
	cancelFn()

	// Clean up our maps
	delete(p.clusters, clusterName)
	delete(p.cancelFns, clusterName)

	log.Info("Successfully disengaged cluster")
	return nil
}

// handleSecretDelete handles the deletion of a kubeconfig secret
func (p *KubeconfigProvider) handleSecretDelete(secret *corev1.Secret) {
	log := p.log.WithValues("secret", types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace})
	log.Info("Handling kubeconfig secret deletion")

	clusterName := secret.Name

	// Use Disengage to handle cleanup
	if err := p.Disengage(context.Background(), clusterName); err != nil {
		if !strings.Contains(err.Error(), "not found") {
			log.Error(err, "failed to disengage cluster")
		}
		return
	}

	// Log current cluster count
	p.lock.RLock()
	clusterCount := len(p.clusters)
	p.lock.RUnlock()
	log.Info("Currently managing clusters", "count", clusterCount)
}

// watchSecrets sets up a watch for secret changes
func (p *KubeconfigProvider) watchSecrets(ctx context.Context, clientset *kubernetes.Clientset, labelSelector string) {
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
				p.handleSecretDelete(secret)
			}
		}

		// If we get here, the watch channel was closed, so we'll retry
		p.log.Info("Watch channel closed, retrying")
		time.Sleep(2 * time.Second)
	}
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
			p.handleSecretDelete(&corev1.Secret{
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
