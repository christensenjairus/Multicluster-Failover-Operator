package controller

// ContextKey defines a type for context keys to avoid string collision
type ContextKey string

// Context keys used by controllers
const (
	ClusterNameKey   ContextKey = "clusterName"
	CrdOnlyKey       ContextKey = "crdOnly"
	SyncModeKey      ContextKey = "syncMode"
	FailoverKey      ContextKey = "failover"
	FailoverGroupKey ContextKey = "failoverGroup"
)
