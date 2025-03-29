package config

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/christensenjairus/Multicluster-Failover-Operator/internal/redis"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// RedisConfig holds Redis configuration from various sources
type RedisConfig struct {
	Hosts    []string
	Port     int
	Password string
	DB       int
	// Leader election specific configs
	LeaderKey  string
	InstanceID string
	logger     logr.Logger
}

// AddFlags adds Redis-related flags to the command line
func (c *RedisConfig) AddFlags() {
	hosts := flag.String("redis-hosts", "localhost", "Comma-separated list of Redis hosts")
	flag.IntVar(&c.Port, "redis-port", 6379, "Redis port")
	flag.StringVar(&c.LeaderKey, "redis-leader-key", "multicluster:leader", "Redis key for leader election")

	// Parse hosts string into slice
	c.Hosts = strings.Split(*hosts, ",")
}

// LoadFromSecret loads Redis configuration from a Kubernetes secret
func (c *RedisConfig) LoadFromSecret(namespace, secretName string) error {
	// Use the default loading rules which will handle multiple kubeconfig paths
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}

	// Create config from loading rules
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
	config, err := kubeConfig.ClientConfig()
	if err != nil {
		return fmt.Errorf("failed to create config from kubeconfig: %w", err)
	}

	// Create Kubernetes client
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Get the secret
	secret, err := clientset.CoreV1().Secrets(namespace).Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get secret %s in namespace %s: %w", secretName, namespace, err)
	}

	c.logger.Info("Found Redis secret", "keys", getSecretKeys(secret))

	// Helper function to get value from either Data or StringData
	getValue := func(key string) (string, bool) {
		// First try StringData
		if val, ok := secret.StringData[key]; ok {
			return val, true
		}
		// Then try Data
		if val, ok := secret.Data[key]; ok {
			return string(val), true
		}
		return "", false
	}

	// Extract Redis configuration from secret
	if hosts, ok := getValue("redis-hosts"); ok {
		c.Hosts = strings.Split(hosts, ",")
		c.logger.Info("Loaded Redis hosts from secret", "hosts", c.Hosts)
	} else {
		c.logger.Info("Warning: redis-hosts not found in secret")
	}
	if port, ok := getValue("redis-port"); ok {
		if portInt, err := strconv.Atoi(port); err == nil {
			c.Port = portInt
			c.logger.Info("Loaded Redis port from secret", "port", c.Port)
		} else {
			c.logger.Info("Warning: invalid redis-port value in secret", "port", port)
		}
	} else {
		c.logger.Info("Warning: redis-port not found in secret")
	}
	if password, ok := getValue("redis-password"); ok {
		c.Password = password
		c.logger.Info("Loaded Redis password from secret", "passwordLength", len(c.Password))
	} else {
		c.logger.Info("Warning: redis-password not found in secret")
	}
	if db, ok := getValue("redis-db"); ok {
		if dbInt, err := strconv.Atoi(db); err == nil {
			c.DB = dbInt
			c.logger.Info("Loaded Redis DB from secret", "db", c.DB)
		} else {
			c.logger.Info("Warning: invalid redis-db value in secret", "db", db)
		}
	} else {
		c.logger.Info("Warning: redis-db not found in secret")
	}
	if leaderKey, ok := getValue("redis-leader-key"); ok {
		c.LeaderKey = leaderKey
		c.logger.Info("Loaded Redis leader key from secret", "key", c.LeaderKey)
	} else {
		c.logger.Info("Warning: redis-leader-key not found in secret")
	}

	// Set instance ID to pod name or hostname + PID
	if podName := os.Getenv("POD_NAME"); podName != "" {
		c.InstanceID = podName
		c.logger.Info("Using pod name as instance ID", "instanceID", c.InstanceID)
	} else if hostname, err := os.Hostname(); err == nil {
		pid := os.Getpid()
		c.InstanceID = fmt.Sprintf("%s-%d", hostname, pid)
		c.logger.Info("Using hostname + PID as instance ID", "instanceID", c.InstanceID)
	} else {
		return fmt.Errorf("neither POD_NAME nor hostname could be determined for instance ID")
	}

	return nil
}

// getSecretKeys returns a slice of keys from both Data and StringData
func getSecretKeys(secret *corev1.Secret) []string {
	keys := make([]string, 0, len(secret.Data)+len(secret.StringData))
	for k := range secret.Data {
		keys = append(keys, k)
	}
	for k := range secret.StringData {
		keys = append(keys, k)
	}
	return keys
}

// ToRedisConfig converts RedisConfig to redis.Config
func (c *RedisConfig) ToRedisConfig() redis.Config {
	return redis.Config{
		Hosts:      c.Hosts,
		Port:       c.Port,
		Password:   c.Password,
		DB:         c.DB,
		LeaderKey:  c.LeaderKey,
		InstanceID: c.InstanceID,
	}
}

// Validate validates the Redis configuration
func (c *RedisConfig) Validate() error {
	if len(c.Hosts) == 0 {
		return fmt.Errorf("redis hosts are required")
	}
	if c.Port <= 0 {
		return fmt.Errorf("redis port must be positive")
	}
	if c.LeaderKey == "" {
		return fmt.Errorf("redis leader key is required")
	}
	if c.InstanceID == "" {
		return fmt.Errorf("instance ID must be set (should be pod name)")
	}
	return nil
}

// SetLogger sets the logger for the RedisConfig
func (c *RedisConfig) SetLogger(logger logr.Logger) {
	c.logger = logger.WithName("redis-config")
}
