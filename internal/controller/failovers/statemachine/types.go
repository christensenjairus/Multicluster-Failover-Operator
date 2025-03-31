package statemachine

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	crdv1alpha1 "github.com/christensenjairus/Multicluster-Failover-Operator/api/v1alpha1"
)

// ClusterTarget specifies which cluster a state should be executed on
type ClusterTarget string

const (
	SourceCluster ClusterTarget = "SOURCE"
	TargetCluster ClusterTarget = "TARGET"
)

// State represents a step in the failover workflow
type State interface {
	// Execute performs the state's work and returns the next state
	Execute(ctx context.Context, data *FailoverContext) (State, error)

	// Name returns a string identifier for this state
	Name() string
}

// WorkflowStep defines a single step in a workflow
type WorkflowStep struct {
	// Name of the state to execute
	Name string

	// Which cluster this step should be executed on
	Target ClusterTarget
}

// Workflow defines a complete failover workflow
type Workflow struct {
	// Name of the workflow
	Name string

	// Steps in the workflow, in order of execution
	Steps []WorkflowStep
}

// FailoverContext contains all data needed during failover
type FailoverContext struct {
	// The failover resource being processed
	Failover *crdv1alpha1.Failover

	// The failover group being processed
	FailoverGroup *crdv1alpha1.FailoverGroup

	// Current state of the failover
	CurrentState string

	// A map of source/target clusters
	Clusters map[string]client.Client

	// Reference to the state machine for accessing other states
	StateMachine *StateMachine

	// Start time of failover
	StartTime time.Time

	// Logger
	Log logr.Logger
}

// StateMachine manages the failover workflow
type StateMachine struct {
	states       map[string]State
	currentState State
	context      *FailoverContext
	log          logr.Logger
	workflow     *Workflow
}

// BaseState provides common functionality for all states
type BaseState struct {
	name string
}

// Name returns the state name
func (b *BaseState) Name() string {
	return b.name
}
