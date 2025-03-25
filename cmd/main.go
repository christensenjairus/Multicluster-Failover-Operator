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
	"crypto/tls"
	"flag"
	"os"
	"strings"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/christensenjairus/Multicluster-Failover-Operator/internal/controller"
	"github.com/christensenjairus/Multicluster-Failover-Operator/internal/multicluster"
	kubeconfig "github.com/christensenjairus/Multicluster-Failover-Operator/providers/kubeconfig"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = zap.New(zap.UseDevMode(true))
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// ControllerSetupFunc is a function that sets up a controller with the manager
type ControllerSetupFunc func(manager.Manager) error

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
		setupLog.Info("using explicitly provided master URL", "master", masterURL)
		if kubeconfigPath != "" {
			setupLog.Info("using kubeconfig file with explicit master", "kubeconfig", kubeconfigPath)
			config, err = clientcmd.BuildConfigFromFlags(masterURL, kubeconfigPath)
		} else {
			config = &rest.Config{
				Host: masterURL,
			}
		}
	} else {
		setupLog.Info("using controller-runtime config handling")
		if kubeconfigPath != "" {
			setupLog.Info("using kubeconfig file", "path", kubeconfigPath)
		}
		config, err = ctrl.GetConfig()
	}

	if err != nil {
		setupLog.Error(err, "unable to get kubernetes configuration")
		os.Exit(1)
	}

	setupLog.Info("successfully connected to kubernetes api", "host", config.Host)

	// Disable HTTP/2 if not explicitly enabled
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

	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

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

	// Create a standard controller-runtime manager
	mgr, err := ctrl.NewManager(config, mgmtOpts)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Create the multicluster manager
	mcManager := multicluster.NewMulticlusterManager(mgr)

	// Create the kubeconfig provider for multicluster discovery
	provider := kubeconfig.New(mcManager, kubeconfig.Options{
		Namespace:         namespace,
		KubeconfigLabel:   "sigs.k8s.io/multicluster-runtime-kubeconfig",
		Scheme:            scheme,
		ConnectionTimeout: 10 * time.Second,
		CacheSyncTimeout:  30 * time.Second,
	})

	// Setup healthz/readyz checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	// Start the provider in a background goroutine
	go func() {
		setupLog.Info("starting kubeconfig provider")
		err := provider.Run(ctx, mcManager)
		if err != nil {
			setupLog.Error(err, "error running provider")
			os.Exit(1)
		}
	}()

	// Setup controllers
	if err := controller.SetupControllers(mcManager); err != nil {
		setupLog.Error(err, "unable to setup controllers")
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
