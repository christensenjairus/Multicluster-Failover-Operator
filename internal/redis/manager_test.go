package redis

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/zapr"
	redisclient "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func checkRedisAvailable() bool {
	client := redisclient.NewClient(&redisclient.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       0,
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err := client.Ping(ctx).Result()
	return err == nil
}

func TestNewManager(t *testing.T) {
	if !checkRedisAvailable() {
		t.Skip("Redis is not available")
	}

	zapLog, _ := zap.NewDevelopment()
	logger := zapr.NewLogger(zapLog)

	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: Config{
				Hosts:      []string{"localhost"},
				Port:       6379,
				Password:   "",
				DB:         0,
				LeaderKey:  "test:leader",
				InstanceID: "test-instance",
			},
			wantErr: false,
		},
		{
			name: "no hosts",
			cfg: Config{
				Hosts:      []string{},
				Port:       6379,
				Password:   "",
				DB:         0,
				LeaderKey:  "test:leader",
				InstanceID: "test-instance",
			},
			wantErr: true,
		},
		{
			name: "invalid port",
			cfg: Config{
				Hosts:      []string{"localhost"},
				Port:       0,
				Password:   "",
				DB:         0,
				LeaderKey:  "test:leader",
				InstanceID: "test-instance",
			},
			wantErr: true,
		},
		{
			name: "no leader key",
			cfg: Config{
				Hosts:      []string{"localhost"},
				Port:       6379,
				Password:   "",
				DB:         0,
				LeaderKey:  "",
				InstanceID: "test-instance",
			},
			wantErr: true,
		},
		{
			name: "no instance ID",
			cfg: Config{
				Hosts:      []string{"localhost"},
				Port:       6379,
				Password:   "",
				DB:         0,
				LeaderKey:  "test:leader",
				InstanceID: "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, err := NewManager(tt.cfg, logger)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, manager)
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, manager)
			assert.NotNil(t, manager.rs)
			assert.Equal(t, tt.cfg.LeaderKey, manager.leaderKey)
			assert.Equal(t, tt.cfg.InstanceID, manager.instanceID)
		})
	}
}

func TestManager_Close(t *testing.T) {
	if !checkRedisAvailable() {
		t.Skip("Redis is not available")
	}

	zapLog, _ := zap.NewDevelopment()
	logger := zapr.NewLogger(zapLog)
	cfg := Config{
		Hosts:      []string{"localhost"},
		Port:       6379,
		Password:   "",
		DB:         0,
		LeaderKey:  "test:leader",
		InstanceID: "test-instance",
	}

	manager, err := NewManager(cfg, logger)
	require.NoError(t, err)
	require.NotNil(t, manager)

	err = manager.Close()
	assert.NoError(t, err)
}

func TestManager_StartLeaderElection(t *testing.T) {
	if !checkRedisAvailable() {
		t.Skip("Redis is not available")
	}

	zapLog, _ := zap.NewDevelopment()
	logger := zapr.NewLogger(zapLog)
	cfg := Config{
		Hosts:      []string{"localhost"},
		Port:       6379,
		Password:   "",
		DB:         0,
		LeaderKey:  "test:leader",
		InstanceID: "test-instance",
	}

	manager, err := NewManager(cfg, logger)
	require.NoError(t, err)
	require.NotNil(t, manager)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	leaderChan := make(chan bool, 1)
	manager.StartLeaderElection(ctx, 1*time.Second, leaderChan)

	// Wait for leadership status
	select {
	case isLeader := <-leaderChan:
		t.Logf("Leadership status: %v", isLeader)
	case <-ctx.Done():
		t.Fatal("Timeout waiting for leadership status")
	}
}

func TestManager_AcquireLeadership(t *testing.T) {
	if !checkRedisAvailable() {
		t.Skip("Redis is not available")
	}

	zapLog, _ := zap.NewDevelopment()
	logger := zapr.NewLogger(zapLog)
	cfg := Config{
		Hosts:      []string{"localhost"},
		Port:       6379,
		Password:   "",
		DB:         0,
		LeaderKey:  "test:leader",
		InstanceID: "test-instance",
	}

	manager, err := NewManager(cfg, logger)
	require.NoError(t, err)
	require.NotNil(t, manager)

	ctx := context.Background()
	err = manager.AcquireLeadership(ctx, 1*time.Second)
	assert.NoError(t, err)
	assert.NotNil(t, manager.mutex)
}

func TestManager_ReleaseLeadership(t *testing.T) {
	if !checkRedisAvailable() {
		t.Skip("Redis is not available")
	}

	zapLog, _ := zap.NewDevelopment()
	logger := zapr.NewLogger(zapLog)
	cfg := Config{
		Hosts:      []string{"localhost"},
		Port:       6379,
		Password:   "",
		DB:         0,
		LeaderKey:  "test:leader",
		InstanceID: "test-instance",
	}

	manager, err := NewManager(cfg, logger)
	require.NoError(t, err)
	require.NotNil(t, manager)

	ctx := context.Background()
	err = manager.ReleaseLeadership(ctx)
	assert.NoError(t, err)
	assert.Nil(t, manager.mutex)
}

func TestManager_IsLeader(t *testing.T) {
	if !checkRedisAvailable() {
		t.Skip("Redis is not available")
	}

	zapLog, _ := zap.NewDevelopment()
	logger := zapr.NewLogger(zapLog)
	cfg := Config{
		Hosts:      []string{"localhost"},
		Port:       6379,
		Password:   "",
		DB:         0,
		LeaderKey:  "test:leader",
		InstanceID: "test-instance",
	}

	manager, err := NewManager(cfg, logger)
	require.NoError(t, err)
	require.NotNil(t, manager)

	ctx := context.Background()
	isLeader, err := manager.IsLeader(ctx)
	assert.NoError(t, err)
	assert.False(t, isLeader)
}

func TestManager_GetCurrentLeader(t *testing.T) {
	if !checkRedisAvailable() {
		t.Skip("Redis is not available")
	}

	zapLog, _ := zap.NewDevelopment()
	logger := zapr.NewLogger(zapLog)
	cfg := Config{
		Hosts:      []string{"localhost"},
		Port:       6379,
		Password:   "",
		DB:         0,
		LeaderKey:  "test:leader",
		InstanceID: "test-instance",
	}

	manager, err := NewManager(cfg, logger)
	require.NoError(t, err)
	require.NotNil(t, manager)

	ctx := context.Background()
	leader, err := manager.GetCurrentLeader(ctx)
	assert.NoError(t, err)
	assert.Empty(t, leader)
}
