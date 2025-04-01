package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-redsync/redsync/v4"
	redsyncredis "github.com/go-redsync/redsync/v4/redis"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	redisclient "github.com/redis/go-redis/v9"
)

// Manager handles Redis operations and leader election
type Manager struct {
	clients    []*redisclient.Client
	logger     logr.Logger
	leaderKey  string
	instanceID string
	rs         *redsync.Redsync
	mutex      *redsync.Mutex
}

// Config holds Redis connection configuration
type Config struct {
	Hosts    []string // List of Redis hosts
	Port     int
	Password string
	DB       int
	// Leader election specific configs
	LeaderKey  string
	InstanceID string
}

// NewManager creates a new Redis manager instance
func NewManager(cfg Config, logger logr.Logger) (*Manager, error) {
	if len(cfg.Hosts) == 0 {
		return nil, fmt.Errorf("at least one Redis host is required")
	}

	// Create Redis clients for each host
	clients := make([]*redisclient.Client, len(cfg.Hosts))
	for i, host := range cfg.Hosts {
		client := redisclient.NewClient(&redisclient.Options{
			Addr:     fmt.Sprintf("%s:%d", host, cfg.Port),
			Password: cfg.Password,
			DB:       cfg.DB,
		})

		// Test connection
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := client.Ping(ctx).Err(); err != nil {
			cancel()
			return nil, fmt.Errorf("failed to connect to Redis at %s: %w", host, err)
		}
		cancel()

		clients[i] = client
		logger.Info("Successfully connected to Redis", "host", host)
	}

	// Create pools for each client
	pools := make([]redsyncredis.Pool, len(clients))
	for i, client := range clients {
		pools[i] = goredis.NewPool(client)
	}

	// Create an instance of redsync with multiple pools for true RedLock
	rs := redsync.New(pools...)

	return &Manager{
		clients:    clients,
		logger:     logger,
		leaderKey:  cfg.LeaderKey,
		instanceID: cfg.InstanceID,
		rs:         rs,
	}, nil
}

// Close closes all Redis connections
func (m *Manager) Close() error {
	// Create a context with a timeout for cleanup
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Release the lock if we have it
	if err := m.ReleaseLeadership(ctx); err != nil {
		m.logger.Error(err, "Failed to release lock during cleanup")
	} else {
		m.logger.Info("Successfully released lock during cleanup")
	}

	// Close all Redis connections
	for _, client := range m.clients {
		if err := client.Close(); err != nil {
			m.logger.Error(err, "Failed to close Redis connection")
		}
	}

	return nil
}

// StartLeaderElection starts the leader election process
func (m *Manager) StartLeaderElection(ctx context.Context, ttl time.Duration, leaderChan chan<- bool) {
	go func() {
		// Try to acquire leadership immediately
		if err := m.AcquireLeadership(ctx, ttl); err != nil {
			leaderChan <- false
		} else {
			leaderChan <- true
		}

		// Probe every 10 seconds
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				m.logger.Info("Leader election context cancelled")
				leaderChan <- false
				return
			case <-ticker.C:
				// Check if we're the leader
				isLeader, err := m.IsLeader(ctx)
				if err != nil {
					m.logger.Error(err, "Failed to check leadership status")
					leaderChan <- false
					continue
				}

				if isLeader {
					// We're the leader, extend TTL
					if err := m.ExtendLeadership(ctx); err != nil {
						m.logger.Error(err, "Failed to extend leadership")
						leaderChan <- false
						continue
					}
					//m.logger.Info("Successfully extended leadership")
					// Don't send true here - we're already the leader
				} else {
					// We're not the leader, try to acquire leadership
					if err := m.AcquireLeadership(ctx, ttl); err != nil {
						leaderChan <- false
						continue
					}
					m.logger.Info("Successfully acquired leadership")
					leaderChan <- true
				}
			}
		}
	}()
}

// AcquireLeadership attempts to acquire leadership using Redis RedLock algorithm
func (m *Manager) AcquireLeadership(ctx context.Context, ttl time.Duration) error {
	// Create a new mutex with our leader key
	mutex := m.rs.NewMutex(
		m.leaderKey,
		redsync.WithExpiry(ttl),
		redsync.WithValue(m.instanceID),
		redsync.WithTries(3),          // Number of times to try to acquire the lock
		redsync.WithDriftFactor(0.01), // Allow 1% clock drift
	)

	// Try to acquire the lock
	if err := mutex.Lock(); err != nil {
		return fmt.Errorf("failed to acquire leadership: %w", err)
	}

	// Store the mutex for later use (extending/releasing)
	m.mutex = mutex
	return nil
}

// ExtendLeadership extends the leadership TTL
func (m *Manager) ExtendLeadership(ctx context.Context) error {
	if m.mutex == nil {
		return fmt.Errorf("no active leadership to extend")
	}

	if ok, err := m.mutex.Extend(); err != nil {
		return fmt.Errorf("failed to extend leadership: %w", err)
	} else if !ok {
		return fmt.Errorf("failed to extend leadership: lock lost")
	}

	return nil
}

// ReleaseLeadership releases leadership
func (m *Manager) ReleaseLeadership(ctx context.Context) error {
	m.logger.Info("Releasing leadership")

	if m.mutex == nil {
		return nil
	}

	if ok, err := m.mutex.Unlock(); err != nil {
		return fmt.Errorf("failed to release leadership: %w", err)
	} else if !ok {
		return fmt.Errorf("failed to release leadership: lock already released")
	}

	m.mutex = nil
	m.logger.Info("Successfully released leadership")
	return nil
}

// IsLeader checks if the current instance is the leader
func (m *Manager) IsLeader(ctx context.Context) (bool, error) {
	if m.mutex == nil {
		return false, nil
	}

	// Check if we still own the lock
	ok, err := m.mutex.Valid()
	if err != nil {
		return false, fmt.Errorf("failed to check leadership status: %w", err)
	}

	return ok, nil
}

// GetCurrentLeader returns the ID of the current leader
func (m *Manager) GetCurrentLeader(ctx context.Context) (string, error) {
	// Get the current value of the lock from the first Redis node
	val, err := m.clients[0].Get(ctx, m.leaderKey).Result()
	if err == redisclient.Nil {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get current leader: %w", err)
	}

	return val, nil
}
