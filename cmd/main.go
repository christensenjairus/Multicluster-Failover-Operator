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

package main

import (
	"context"
	"flag"
	"os"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	"k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	ctrl "sigs.k8s.io/controller-runtime"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	// Import your controllers here <--------------------------------
	"github.com/christensenjairus/Multicluster-Failover-Operator/api/v1alpha1"
	internalconfig "github.com/christensenjairus/Multicluster-Failover-Operator/internal/config"
	"github.com/christensenjairus/Multicluster-Failover-Operator/internal/controller/failovergroups"
	"github.com/christensenjairus/Multicluster-Failover-Operator/internal/controller/failovers"
	"github.com/christensenjairus/Multicluster-Failover-Operator/internal/redis"

	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	kubeconfigprovider "sigs.k8s.io/multicluster-runtime/providers/kubeconfig"
)

func main() {
	var (
		namespace             string
		kubeconfigSecretLabel string
		kubeconfigSecretKey   string
		redisSecretName       string
	)

	flag.StringVar(&namespace, "namespace", "multicluster-failover-operator-system", "Namespace where kubeconfig secrets are stored")
	flag.StringVar(&kubeconfigSecretLabel, "kubeconfig-label", "sigs.k8s.io/multicluster-runtime-kubeconfig",
		"Label used to identify secrets containing kubeconfig data")
	flag.StringVar(&kubeconfigSecretKey, "kubeconfig-key", "kubeconfig", "Key in the secret data that contains the kubeconfig")
	flag.StringVar(&redisSecretName, "redis-secret-name", "redis-config", "Name of the Redis configuration secret")

	// Initialize Redis configuration
	redisConfig := &internalconfig.RedisConfig{}
	redisConfig.AddFlags()

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrllog.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	entryLog := ctrllog.Log.WithName("entrypoint")
	ctx := ctrl.SetupSignalHandler()

	entryLog.Info("Starting application", "namespace", namespace, "kubeconfigSecretLabel", kubeconfigSecretLabel)

	// Load Redis configuration from secret
	if err := redisConfig.LoadFromSecret(namespace, redisSecretName); err != nil {
		entryLog.Info("Warning: Failed to load Redis configuration from secret", "error", err)
	}

	// Validate Redis configuration
	if err := redisConfig.Validate(); err != nil {
		entryLog.Error(err, "Invalid Redis configuration")
		os.Exit(1)
	}

	// Create Redis manager
	redisManager, err := redis.NewManager(redisConfig.ToRedisConfig(), entryLog)
	if err != nil {
		entryLog.Error(err, "Failed to create Redis manager")
		os.Exit(1)
	}
	defer redisManager.Close()

	// Create a channel for leader status
	leaderChan := make(chan bool, 1)

	// Start Redis leader election
	redisManager.StartLeaderElection(ctx, 180*time.Second, leaderChan)

	// Create the kubeconfig provider with options
	providerOpts := kubeconfigprovider.Options{
		Namespace:             namespace,
		KubeconfigSecretLabel: kubeconfigSecretLabel,
		KubeconfigSecretKey:   kubeconfigSecretKey,
	}

	// Create the provider and manager
	provider := kubeconfigprovider.New(providerOpts)

	// Add our API types to the scheme
	if err := v1alpha1.AddToScheme(scheme.Scheme); err != nil {
		entryLog.Error(err, "Failed to add API types to scheme")
		os.Exit(1)
	}

	// Modify manager options to avoid waiting for cache sync
	managerOpts := manager.Options{
		// Don't block main thread on leader election
		LeaderElection: false,
		// Add the scheme
		Scheme: scheme.Scheme,
	}

	mgr, err := mcmanager.New(ctrl.GetConfigOrDie(), provider, managerOpts)
	if err != nil {
		entryLog.Error(err, "Unable to create manager")
		os.Exit(1)
	}
	failoverGroupController := failovergroups.NewFailoverGroupReconciler(mgr)
	if err := mgr.Add(failoverGroupController); err != nil {
		entryLog.Error(err, "Unable to add failover group controller")
		os.Exit(1)
	}
	failoverController := failovers.NewFailoverReconciler(mgr)
	if err := mgr.Add(failoverController); err != nil {
		entryLog.Error(err, "Unable to add failover controller")
		os.Exit(1)
	}

	// Create channels to control manager and provider
	managerCtx, managerCancel := context.WithCancel(ctx)
	providerCtx, providerCancel := context.WithCancel(ctx)

	entryLog.Info("Acquiring leader lock...")

	// Handle leadership changes
	go func() {
		wasLeader := false
		for {
			select {
			case <-ctx.Done():
				entryLog.Info("Context cancelled, stopping manager and provider")
				managerCancel()
				providerCancel()
				return
			case isLeader := <-leaderChan:
				if isLeader {
					entryLog.Info("Acquired leadership, starting manager and provider")
					// Start manager
					go func() {
						if err := mgr.Start(managerCtx); err != nil {
							entryLog.Error(err, "Error running manager")
						}
					}()

					// Start provider
					entryLog.Info("Starting provider")
					go func() {
						if err := provider.Run(providerCtx, mgr); err != nil && providerCtx.Err() == nil {
							entryLog.Error(err, "Provider exited with error")
						}
					}()
					wasLeader = true
				} else {
					if wasLeader {
						entryLog.Info("Lost leadership, stopping manager and provider")
					}
					managerCancel()
					providerCancel()
					// Create new contexts for next time
					managerCtx, managerCancel = context.WithCancel(ctx)
					providerCtx, providerCancel = context.WithCancel(ctx)
					wasLeader = false
				}
			}
		}
	}()

	// Wait for context cancellation
	<-ctx.Done()
	entryLog.Info("Shutting down...")
}
