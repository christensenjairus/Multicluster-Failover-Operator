package statemachine

import (
	"fmt"

	crdv1alpha1 "github.com/christensenjairus/Multicluster-Failover-Operator/api/v1alpha1"
)

// GetWorkflow returns the appropriate workflow for the given failover mode
func GetWorkflow(mode crdv1alpha1.FailoverMode) *Workflow {
	switch mode {
	case crdv1alpha1.FailoverModeConsistency:
		return &ConsistencyWorkflow
	default:
		return nil
	}
}

// ConsistencyWorkflow defines the basic failover workflow
var ConsistencyWorkflow = Workflow{
	Name: "Consistency",
	Steps: []WorkflowStep{
		{
			Name:   "PreFlightChecks",
			Target: SourceCluster,
		},
		{
			Name:   "ScaleWorkload",
			Target: SourceCluster,
		},
		{
			Name:   "Complete",
			Target: SourceCluster,
		},
	},
}

// ValidateWorkflow validates that a workflow is properly defined
func ValidateWorkflow(workflow *Workflow) error {
	if workflow == nil {
		return fmt.Errorf("workflow is nil")
	}
	if len(workflow.Steps) == 0 {
		return fmt.Errorf("workflow has no steps")
	}
	return nil
}
