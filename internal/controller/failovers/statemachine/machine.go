package statemachine

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	crdv1alpha1 "github.com/christensenjairus/Multicluster-Failover-Operator/api/v1alpha1"
)

// NewStateMachine creates a new state machine
func NewStateMachine(failover *crdv1alpha1.Failover, failoverGroup *crdv1alpha1.FailoverGroup, clusters map[string]client.Client, log logr.Logger) *StateMachine {
	// Get the current cluster name by finding the cluster that has the failover
	var currentCluster string
	for clusterName, clusterClient := range clusters {
		// Try to get the failover from this cluster
		err := clusterClient.Get(context.Background(), client.ObjectKey{
			Namespace: failover.Namespace,
			Name:      failover.Name,
		}, &crdv1alpha1.Failover{})

		if err == nil {
			currentCluster = clusterName
			break
		}
	}

	if currentCluster == "" {
		log.Error(fmt.Errorf("could not determine current cluster"), "failed to determine current cluster")
		return nil
	}

	// Check if this cluster is the source of truth
	if currentCluster != failover.Spec.TargetCluster {
		log.V(2).Info("Skipping state machine - not the source of truth cluster",
			"currentCluster", currentCluster,
			"sourceCluster", failover.Spec.TargetCluster)
		return nil
	}

	sm := &StateMachine{
		states: make(map[string]State),
		log:    log,
	}

	// Create failover context
	sm.context = &FailoverContext{
		Failover:      failover,
		FailoverGroup: failoverGroup,
		Clusters:      clusters,
		Log:           log,
		StartTime:     time.Now(),
	}

	// Get the workflow
	sm.workflow = GetWorkflow(failover.Spec.FailoverMode)
	if sm.workflow == nil {
		log.Error(fmt.Errorf("invalid failover mode: %s", failover.Spec.FailoverMode), "invalid failover mode")
		return nil
	}

	// Register all states
	sm.registerStates()

	// Set initial state
	if initialState, exists := sm.states[sm.workflow.Steps[0].Name]; exists {
		sm.currentState = initialState
	} else {
		log.Error(fmt.Errorf("initial state not found"), "failed to initialize state machine")
	}

	return sm
}

// registerStates registers all states used in the workflow
func (sm *StateMachine) registerStates() {
	sm.states["PreFlightChecks"] = NewPreFlightChecksState(sm.context.Failover, sm.context.FailoverGroup, sm.context.Clusters, sm.log)
	sm.states["ScaleWorkload"] = NewScaleWorkloadState(sm.context.Failover, sm.context.FailoverGroup, sm.context.Clusters, sm.log)
	sm.states["Complete"] = NewCompleteState(sm.context.Failover, sm.context.FailoverGroup, sm.context.Clusters, sm.log)
}

// Execute runs the state machine
func (sm *StateMachine) Execute(ctx context.Context) error {
	if sm == nil {
		return fmt.Errorf("state machine is nil")
	}

	if sm.currentState == nil {
		return fmt.Errorf("no current state")
	}

	// Execute current state
	_, err := sm.currentState.Execute(ctx, sm.context)
	if err != nil {
		return err
	}

	// Find the next state in the workflow
	currentIndex := -1
	for i, step := range sm.workflow.Steps {
		if step.Name == sm.currentState.Name() {
			currentIndex = i
			break
		}
	}

	if currentIndex == -1 {
		return fmt.Errorf("current state %s not found in workflow", sm.currentState.Name())
	}

	// If we're at the last step, we're done
	if currentIndex == len(sm.workflow.Steps)-1 {
		sm.log.Info("State machine completed successfully")
		return nil
	}

	// Move to the next state
	nextStateName := sm.workflow.Steps[currentIndex+1].Name
	nextState, exists := sm.states[nextStateName]
	if !exists {
		return fmt.Errorf("next state %s not found", nextStateName)
	}

	sm.currentState = nextState
	return nil
}

// GetCurrentState returns the current state of the state machine
func (sm *StateMachine) GetCurrentState() State {
	return sm.currentState
}
