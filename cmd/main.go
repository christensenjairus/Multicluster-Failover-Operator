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

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	crdv1alpha1 "github.com/christensenjairus/Multicluster-Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Multicluster-Failover-Operator/internal/controller"
	"github.com/christensenjairus/Multicluster-Failover-Operator/providers/kubeconfigs"

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

// MulticlusterManager implements the kubeconfigs.Manager interface
type MulticlusterManager struct {
	mu       sync.RWMutex
	clusters map[string]cluster.Cluster
	scheme   *runtime.Scheme
}

// NewMulticlusterManager creates a new multicluster manager
func NewMulticlusterManager(scheme *runtime.Scheme) *MulticlusterManager {
	return &MulticlusterManager{
		clusters: make(map[string]cluster.Cluster),
		scheme:   scheme,
	}
}

// Engage registers a cluster with the manager
func (m *MulticlusterManager) Engage(ctx context.Context, name string, cl cluster.Cluster) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.clusters[name] = cl
	setupLog.Info("Engaged cluster", "name", name)

	return nil
}

// GetCluster retrieves a cluster by name
func (m *MulticlusterManager) GetCluster(name string) (cluster.Cluster, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cl, ok := m.clusters[name]
	return cl, ok
}

// Get implements the kubeconfigs.Manager interface
func (m *MulticlusterManager) Get(ctx context.Context, name string) (cluster.Cluster, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cl, ok := m.clusters[name]
	if !ok {
		return nil, fmt.Errorf("cluster %s not found", name)
	}
	return cl, nil
}

// Start starts the manager
func (m *MulticlusterManager) Start(ctx context.Context) error {
	<-ctx.Done()
	return nil
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

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		// TODO(user): TLSOpts is used to allow configuring the TLS config used for the server. If certificates are
		// not provided, self-signed certificates will be generated by default. This option is not recommended for
		// production environments as self-signed certificates do not offer the same level of trust and security
		// as certificates issued by a trusted Certificate Authority (CA). The primary risk is potentially allowing
		// unauthorized access to sensitive metrics data. Consider replacing with CertDir, CertName, and KeyName
		// to provide certificates, ensuring the server communicates using trusted and secure certificates.
		TLSOpts: tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// Get the root context for the whole application
	ctx := ctrl.SetupSignalHandler()

	mgr, err := ctrl.NewManager(config, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "cb9167b4.hahomelabs.com",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Setup multicluster functionality
	namespace, err := getOperatorNamespace()
	if err != nil {
		setupLog.Error(err, "unable to determine operator namespace")
		os.Exit(1)
	}

	setupLog.Info("initializing multicluster support", "namespace", namespace)

	// Create our custom multicluster manager
	mcMgr := NewMulticlusterManager(scheme)

	// Initialize kubeconfig provider with static label, auto-detected namespace, and our config
	provider := kubeconfigs.New(mgr.GetClient(), kubeconfigs.Options{
		Namespace:       namespace,
		KubeconfigLabel: "sigs.k8s.io/multicluster-runtime-kubeconfig",
		// We don't need to pass config explicitly as the controller-runtime client already uses it
	})

	// Start provider in a separate goroutine
	go func() {
		setupLog.Info("starting kubeconfig provider")
		if err := provider.Run(ctx, mcMgr); err != nil {
			setupLog.Error(err, "problem running kubeconfig provider")
			os.Exit(1)
		}
	}()

	if err = (&controller.FailoverGroupReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		MCManager: mcMgr,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "FailoverGroup")
		os.Exit(1)
	}

	if err = (&controller.FailoverReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		MCManager: mcMgr,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Failover")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder

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
