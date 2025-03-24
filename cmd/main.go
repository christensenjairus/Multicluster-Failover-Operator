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

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	crdv1alpha1 "github.com/christensenjairus/Multicluster-Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Multicluster-Failover-Operator/internal/controller"
	"github.com/christensenjairus/Multicluster-Failover-Operator/providers/kubeconfigs"

	// Import multicluster-runtime packages

	corev1 "k8s.io/api/core/v1"

	// +kubebuilder:scaffold:imports
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(crdv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var masterURL string
	var tlsOpts []func(*tls.Config)

	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig.")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Create config with explicitly provided kubeconfig/master
	// Note: controller-runtime already handles the --kubeconfig flag
	var config *rest.Config
	var err error

	// Get kubeconfig path from the flag that controller-runtime registers
	kubeconfigPath := os.Getenv("KUBECONFIG")
	for i := 1; i < len(os.Args); i++ {
		if strings.HasPrefix(os.Args[i], "--kubeconfig=") {
			kubeconfigPath = strings.TrimPrefix(os.Args[i], "--kubeconfig=")
			break
		}
		if os.Args[i] == "--kubeconfig" && i+1 < len(os.Args) {
			kubeconfigPath = os.Args[i+1]
			break
		}
	}

	if masterURL != "" {
		setupLog.Info("Using explicitly provided master URL", "master", masterURL)
		// Use explicit master URL with kubeconfig if provided
		if kubeconfigPath != "" {
			setupLog.Info("Using kubeconfig file with explicit master", "kubeconfig", kubeconfigPath)
			config, err = clientcmd.BuildConfigFromFlags(masterURL, kubeconfigPath)
		} else {
			// Just use the master URL
			config = &rest.Config{
				Host: masterURL,
			}
		}
	} else {
		// Use controller-runtime's standard config handling
		setupLog.Info("Using controller-runtime config handling")
		if kubeconfigPath != "" {
			setupLog.Info("Using kubeconfig file", "path", kubeconfigPath)
		}
		config, err = ctrl.GetConfig()
	}

	if err != nil {
		setupLog.Error(err, "unable to get kubernetes configuration")
		os.Exit(1)
	}

	setupLog.Info("Successfully connected to Kubernetes API", "host", config.Host)

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: tlsOpts,
	})

	// Metrics endpoint options
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// Get the root context for the whole application
	ctx := ctrl.SetupSignalHandler()

	// Determine the namespace for kubeconfig discovery
	namespace, err := getOperatorNamespace()
	if err != nil {
		setupLog.Error(err, "unable to determine operator namespace")
		os.Exit(1)
	}

	setupLog.Info("initializing multicluster support", "namespace", namespace)

	// Create standard manager options
	mgmtOpts := manager.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "cb9167b4.hahomelabs.com",
	}

	// First, create a standard controller-runtime manager
	// This is for the conventional controller approach
	mgr, err := ctrl.NewManager(config, mgmtOpts)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Now let's set up the multicluster part
	// Create the kubeconfig provider for multicluster discovery
	provider := kubeconfigs.New(mgr.GetClient(), kubeconfigs.Options{
		Namespace:       namespace,
		KubeconfigLabel: "sigs.k8s.io/multicluster-runtime-kubeconfig",
		Scheme:          scheme,
	})

	// Create a multicluster reconciler
	mcReconciler := &MulticlusterReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		clusters: make(map[string]cluster.Cluster),
	}

	// Now we set up the controller in the standard way first
	if err := (&controller.FailoverGroupReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		MCReconciler: mcReconciler,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create failovergroup controller", "controller", "FailoverGroup")
		os.Exit(1)
	}

	if err := (&controller.FailoverReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		MCReconciler: mcReconciler,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create failover controller", "controller", "Failover")
		os.Exit(1)
	}

	// Start the provider in a background goroutine
	go func() {
		setupLog.Info("starting kubeconfig provider")
		// Create a proper adapter that implements the Manager interface
		managerAdapter := NewKubeconfigClusterManager(mgr, mcReconciler)
		err := provider.Run(ctx, managerAdapter)
		if err != nil {
			setupLog.Error(err, "Error running provider")
			os.Exit(1)
		}
	}()

	// Setup healthz/readyz checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// getOperatorNamespace returns the namespace the operator is currently running in.
func getOperatorNamespace() (string, error) {
	// Check if running in a pod
	ns, found := os.LookupEnv("POD_NAMESPACE")
	if found {
		return ns, nil
	}

	// If not running in a pod, try to get from the service account namespace
	nsBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err == nil {
		return string(nsBytes), nil
	}

	// Default to the standard operator namespace
	return "multicluster-failover-operator-system", nil
}

// KubeconfigClusterManager implements kubeconfigs.ClusterManager for cluster management
type KubeconfigClusterManager struct {
	manager.Manager
	Client     client.Client
	lock       sync.RWMutex
	clusters   map[string]cluster.Cluster
	reconciler *MulticlusterReconciler
}

// NewKubeconfigClusterManager creates a new KubeconfigClusterManager
func NewKubeconfigClusterManager(mgr manager.Manager, reconciler *MulticlusterReconciler) *KubeconfigClusterManager {
	return &KubeconfigClusterManager{
		Manager:    mgr,
		Client:     mgr.GetClient(),
		clusters:   make(map[string]cluster.Cluster),
		reconciler: reconciler,
	}
}

// GetCluster returns the cluster with the given name.
func (a *KubeconfigClusterManager) GetCluster(ctx context.Context, name string) (cluster.Cluster, error) {
	a.lock.RLock()
	defer a.lock.RUnlock()

	if cl, exists := a.clusters[name]; exists {
		setupLog.Info("Found existing cluster", "name", name)
		return cl, nil
	}
	return nil, fmt.Errorf("cluster %s not found", name)
}

// Engage registers a new cluster with the adapter
func (a *KubeconfigClusterManager) Engage(ctx context.Context, name string, cl cluster.Cluster) error {
	a.lock.Lock()
	defer a.lock.Unlock()

	setupLog.Info("Engaging new cluster", "name", name)

	// Store the cluster
	if a.clusters == nil {
		a.clusters = make(map[string]cluster.Cluster)
	}
	a.clusters[name] = cl

	// Initialize the multicluster reconciler if needed
	if a.reconciler != nil {
		// Tell the reconciler about the new cluster
		a.reconciler.RegisterCluster(name, cl)
	} else {
		setupLog.Error(nil, "Reconciler not initialized, can't register cluster", "name", name)
	}

	setupLog.Info("New cluster engaged", "name", name, "totalClusters", len(a.clusters))

	// Log all clusters after a new one is added
	if a.reconciler != nil {
		a.reconciler.ListClustersWithLog()
	}

	return nil
}

// Add implements manager.Manager
func (a *KubeconfigClusterManager) Add(runnable manager.Runnable) error {
	return nil
}

// Disengage removes a cluster from the manager
func (a *KubeconfigClusterManager) Disengage(ctx context.Context, name string) error {
	a.lock.Lock()
	defer a.lock.Unlock()

	if a.reconciler != nil {
		a.reconciler.UnregisterCluster(name)
	}

	delete(a.clusters, name)
	return nil
}

// MulticlusterReconciler handles reconciliations across multiple clusters
type MulticlusterReconciler struct {
	Client   client.Client
	Scheme   *runtime.Scheme
	lock     sync.RWMutex
	clusters map[string]cluster.Cluster
}

// RegisterCluster registers a new cluster with the reconciler
func (r *MulticlusterReconciler) RegisterCluster(name string, cl cluster.Cluster) {
	r.lock.Lock()
	defer r.lock.Unlock()

	setupLog.Info("Registering cluster with reconciler", "name", name)

	if r.clusters == nil {
		r.clusters = make(map[string]cluster.Cluster)
	}

	// Verify the cluster client is working
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try a quick test
	nodes := &corev1.NodeList{}
	err := cl.GetClient().List(ctx, nodes, client.Limit(1))
	if err != nil {
		setupLog.Error(err, "Cluster client test failed during registration", "name", name)
		// Still register it, but log the error
	} else {
		setupLog.Info("Cluster client test successful during registration", "name", name, "nodeCount", len(nodes.Items))
	}

	r.clusters[name] = cl
	setupLog.Info("Registered cluster with reconciler", "name", name, "totalClusters", len(r.clusters))

	// Log all clusters after registration
	clusterNames := make([]string, 0, len(r.clusters))
	for name := range r.clusters {
		clusterNames = append(clusterNames, name)
	}
	setupLog.Info("Current clusters after registration",
		"totalClusters", len(r.clusters),
		"clusterNames", strings.Join(clusterNames, ", "))
}

// ListClusters returns a list of all registered clusters
func (r *MulticlusterReconciler) ListClusters() map[string]cluster.Cluster {
	r.lock.RLock()
	defer r.lock.RUnlock()

	result := make(map[string]cluster.Cluster, len(r.clusters))
	for name, cl := range r.clusters {
		result[name] = cl
	}

	return result
}

// ListClustersWithLog returns a list of all registered clusters and logs them
func (r *MulticlusterReconciler) ListClustersWithLog() map[string]cluster.Cluster {
	r.lock.RLock()
	defer r.lock.RUnlock()

	result := make(map[string]cluster.Cluster, len(r.clusters))
	for name, cl := range r.clusters {
		result[name] = cl
	}

	clusterNames := make([]string, 0, len(result))
	for name := range result {
		clusterNames = append(clusterNames, name)
	}
	setupLog.Info("Listing clusters from reconciler",
		"totalClusters", len(result),
		"clusterNames", strings.Join(clusterNames, ", "))

	return result
}

// UnregisterCluster removes a cluster from the reconciler
func (r *MulticlusterReconciler) UnregisterCluster(name string) {
	r.lock.Lock()
	defer r.lock.Unlock()

	delete(r.clusters, name)
	setupLog.Info("Unregistered cluster", "name", name)
}
