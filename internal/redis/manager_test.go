package redis

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestLogger creates a simple test logger
func createTestLogger() logr.Logger {
	return logr.New(&testLogger{})
}

// testLogger implements logr.LogSink
type testLogger struct{}

func (l *testLogger) Init(info logr.RuntimeInfo)                                {}
func (l *testLogger) Enabled(level int) bool                                    { return true }
func (l *testLogger) Info(level int, msg string, keysAndValues ...interface{})  {}
func (l *testLogger) Error(err error, msg string, keysAndValues ...interface{}) {}
func (l *testLogger) WithValues(keysAndValues ...interface{}) logr.LogSink      { return l }
func (l *testLogger) WithName(name string) logr.LogSink                         { return l }

func TestManager_LeaderElection(t *testing.T) {
	// Create a test Redis client
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	// Clean up any existing test keys
	ctx := context.Background()
	client.Del(ctx, "test:leader")

	// Create a test logger
	logger := createTestLogger()

	// Create a test manager
	manager, err := NewManager(Config{
		Host:       "localhost",
		Port:       6379,
		LeaderKey:  "test:leader",
		InstanceID: "test-instance",
	}, logger)
	require.NoError(t, err)
	defer manager.Close()

	// Test acquiring leadership
	err = manager.AcquireLeadership(ctx, 30*time.Second)
	require.NoError(t, err)

	// Verify we are the leader
	isLeader, err := manager.IsLeader(ctx)
	require.NoError(t, err)
	assert.True(t, isLeader)

	// Test releasing leadership
	err = manager.ReleaseLeadership(ctx)
	require.NoError(t, err)

	// Verify we are no longer the leader
	isLeader, err = manager.IsLeader(ctx)
	require.NoError(t, err)
	assert.False(t, isLeader)
}

func TestRedisManager(t *testing.T) {
	// Create a test Redis client
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	// Clean up any existing test keys
	ctx := context.Background()
	client.Del(ctx, "test:leader")

	// Create a test logger
	logger := createTestLogger()

	// Create a test manager
	manager, err := NewManager(Config{
		Host:       "localhost",
		Port:       6379,
		LeaderKey:  "test:leader",
		InstanceID: "test-instance",
	}, logger)
	require.NoError(t, err)
	defer manager.Close()

	// Test acquiring leadership
	err = manager.AcquireLeadership(ctx, 30*time.Second)
	require.NoError(t, err)

	// Verify we are the leader
	isLeader, err := manager.IsLeader(ctx)
	require.NoError(t, err)
	assert.True(t, isLeader)

	// Test releasing leadership
	err = manager.ReleaseLeadership(ctx)
	require.NoError(t, err)

	// Verify we are no longer the leader
	isLeader, err = manager.IsLeader(ctx)
	require.NoError(t, err)
	assert.False(t, isLeader)
}

func TestRedisManager_Concurrent(t *testing.T) {
	// Create a test Redis client
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	// Clean up any existing test keys
	ctx := context.Background()
	client.Del(ctx, "test:leader")

	// Create a test logger
	logger := createTestLogger()

	// Create two test managers
	manager1, err := NewManager(Config{
		Host:       "localhost",
		Port:       6379,
		LeaderKey:  "test:leader",
		InstanceID: "test-instance-1",
	}, logger)
	require.NoError(t, err)
	defer manager1.Close()

	manager2, err := NewManager(Config{
		Host:       "localhost",
		Port:       6379,
		LeaderKey:  "test:leader",
		InstanceID: "test-instance-2",
	}, logger)
	require.NoError(t, err)
	defer manager2.Close()

	// Test concurrent leadership acquisition
	err = manager1.AcquireLeadership(ctx, 30*time.Second)
	require.NoError(t, err)

	// Verify manager1 is the leader
	isLeader, err := manager1.IsLeader(ctx)
	require.NoError(t, err)
	assert.True(t, isLeader)

	// Try to acquire leadership with manager2
	err = manager2.AcquireLeadership(ctx, 30*time.Second)
	require.NoError(t, err)

	// Verify manager2 is now the leader
	isLeader, err = manager2.IsLeader(ctx)
	require.NoError(t, err)
	assert.True(t, isLeader)

	// Verify manager1 is no longer the leader
	isLeader, err = manager1.IsLeader(ctx)
	require.NoError(t, err)
	assert.False(t, isLeader)
}
