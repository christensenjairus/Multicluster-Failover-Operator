package statemachine

import (
	"context"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	crdv1alpha1 "github.com/christensenjairus/Multicluster-Failover-Operator/api/v1alpha1"
)

// CompleteState is a marker state that indicates the failover process is complete
type CompleteState struct {
	BaseState
}

// NewCompleteState creates a new Complete state
func NewCompleteState(failover *crdv1alpha1.Failover, failoverGroup *crdv1alpha1.FailoverGroup, clusters map[string]client.Client, log logr.Logger) *CompleteState {
	return &CompleteState{
		BaseState: BaseState{
			name: "Complete",
		},
	}
}

// Execute is a no-op since completion is handled by the controller
func (s *CompleteState) Execute(ctx context.Context, data *FailoverContext) (State, error) {
	return nil, nil
}

// Rollback performs the rollback operation for this state
func (s *CompleteState) Rollback(ctx context.Context, data *FailoverContext) error {
	return nil
}
