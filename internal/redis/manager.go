package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/redis/go-redis/v9"
)

// Manager handles Redis operations and leader election
type Manager struct {
	client     *redis.Client
	logger     logr.Logger
	leaderKey  string
	instanceID string
}

// Config holds Redis connection configuration
type Config struct {
	Host     string
	Port     int
	Password string
	DB       int
	// Leader election specific configs
	LeaderKey  string
	InstanceID string
}

// NewManager creates a new Redis manager instance
func NewManager(cfg Config, logger logr.Logger) (*Manager, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	logger.Info("Successfully connected to Redis")

	return &Manager{
		client:     client,
		logger:     logger,
		leaderKey:  cfg.LeaderKey,
		instanceID: cfg.InstanceID,
	}, nil
}

// Close closes the Redis connection
func (m *Manager) Close() error {
	// Create a context with a timeout for cleanup
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Check if we're the leader and release if we are
	isLeader, err := m.IsLeader(ctx)
	if err != nil {
		m.logger.Error(err, "Failed to check leadership status during cleanup")
	} else if isLeader {
		if err := m.ReleaseLeadership(ctx); err != nil {
			m.logger.Error(err, "Failed to release leadership during cleanup")
		} else {
			m.logger.Info("Successfully released leadership during cleanup")
		}
	}

	// Close the Redis connection
	return m.client.Close()
}

// StartLeaderElection starts the leader election process
func (m *Manager) StartLeaderElection(ctx context.Context, ttl time.Duration, leaderChan chan<- bool) {
	go func() {
		// Try to acquire leadership immediately
		if err := m.AcquireLeadership(ctx, ttl); err != nil {
			m.logger.Info("Not the leader, waiting for next probe", "error", err)
			leaderChan <- false
		} else {
			m.logger.Info("Successfully acquired initial leadership")
			leaderChan <- true
		}

		// Probe every 30 seconds
		ticker := time.NewTicker(30 * time.Second)
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
					success, err := m.client.Expire(ctx, m.leaderKey, ttl).Result()
					if err != nil {
						m.logger.Error(err, "Failed to extend leadership")
						leaderChan <- false
						continue
					}
					if !success {
						m.logger.Info("Failed to extend leadership, key may have been deleted")
						leaderChan <- false
						continue
					}
					m.logger.Info("Successfully extended leadership", "ttl", ttl)
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

// AcquireLeadership attempts to acquire leadership
func (m *Manager) AcquireLeadership(ctx context.Context, ttl time.Duration) error {
	m.logger.Info("Attempting to acquire leadership", "ttl", ttl)

	// First check if there's an existing leader
	currentLeader, err := m.client.Get(ctx, m.leaderKey).Result()
	if err == nil {
		// Key exists, check if we're already the leader
		if currentLeader == m.instanceID {
			m.logger.Info("Already the leader, extending TTL")
			success, err := m.client.Expire(ctx, m.leaderKey, ttl).Result()
			if err != nil {
				return fmt.Errorf("failed to extend leadership TTL: %w", err)
			}
			if !success {
				return fmt.Errorf("failed to extend leadership TTL")
			}
			return nil
		}
		// Someone else is the leader
		return fmt.Errorf("cannot acquire leadership: current leader is %s", currentLeader)
	}
	if err != redis.Nil {
		return fmt.Errorf("failed to check current leader: %w", err)
	}

	// No leader exists, try to acquire leadership
	success, err := m.client.SetNX(ctx, m.leaderKey, m.instanceID, ttl).Result()
	if err != nil {
		return fmt.Errorf("failed to acquire leadership: %w", err)
	}
	if !success {
		return fmt.Errorf("failed to acquire leadership: another instance became leader")
	}

	m.logger.Info("Successfully acquired leadership", "ttl", ttl)
	return nil
}

// ReleaseLeadership releases leadership
func (m *Manager) ReleaseLeadership(ctx context.Context) error {
	m.logger.Info("Releasing leadership")
	success, err := m.client.Del(ctx, m.leaderKey).Result()
	if err != nil {
		return fmt.Errorf("failed to release leadership: %w", err)
	}
	if success != 1 {
		return fmt.Errorf("failed to release leadership: unexpected result %d", success)
	}
	m.logger.Info("Successfully released leadership")
	return nil
}

// IsLeader checks if the current instance is the leader
func (m *Manager) IsLeader(ctx context.Context) (bool, error) {
	currentLeader, err := m.client.Get(ctx, m.leaderKey).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to check current leader: %w", err)
	}

	return currentLeader == m.instanceID, nil
}

// GetCurrentLeader returns the ID of the current leader
func (m *Manager) GetCurrentLeader(ctx context.Context) (string, error) {
	currentLeader, err := m.client.Get(ctx, m.leaderKey).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get current leader: %w", err)
	}

	return currentLeader, nil
}
