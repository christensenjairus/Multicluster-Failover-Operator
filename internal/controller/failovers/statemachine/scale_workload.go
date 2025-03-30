package statemachine

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	crdv1alpha1 "github.com/christensenjairus/Multicluster-Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Multicluster-Failover-Operator/internal/controller/deployments"
	"github.com/christensenjairus/Multicluster-Failover-Operator/internal/controller/statefulsets"
)

// ScaleWorkloadState handles scaling the workload
type ScaleWorkloadState struct {
	BaseState
	failover      *crdv1alpha1.Failover
	failoverGroup *crdv1alpha1.FailoverGroup
	clusters      map[string]client.Client
	log           logr.Logger
}

// NewScaleWorkloadState creates a new ScaleWorkload state
func NewScaleWorkloadState(failover *crdv1alpha1.Failover, failoverGroup *crdv1alpha1.FailoverGroup, clusters map[string]client.Client, log logr.Logger) *ScaleWorkloadState {
	return &ScaleWorkloadState{
		BaseState: BaseState{
			name: "ScaleWorkload",
		},
		failover:      failover,
		failoverGroup: failoverGroup,
		clusters:      clusters,
		log:           log,
	}
}

// Execute scales the workload
func (s *ScaleWorkloadState) Execute(ctx context.Context, data *FailoverContext) (State, error) {
	s.log.Info("Executing workload scaling")

	// Get the cluster client
	clusterClient, exists := s.clusters[s.failoverGroup.Status.GlobalState.ActiveCluster]
	if !exists {
		return nil, fmt.Errorf("active cluster %s not found", s.failoverGroup.Status.GlobalState.ActiveCluster)
	}

	// Create managers
	deploymentManager := deployments.NewManager(clusterClient)
	statefulsetManager := statefulsets.NewManager(clusterClient)

	// Scale each workload
	for _, workload := range s.failoverGroup.Spec.Workloads {
		s.log.Info("Scaling workload", "workload", workload.Name)

		var err error
		switch workload.Kind {
		case "Deployment":
			err = deploymentManager.ScaleDown(ctx, workload.Name, s.failover.Namespace)
		case "StatefulSet":
			err = statefulsetManager.ScaleDown(ctx, workload.Name, s.failover.Namespace)
		default:
			return nil, fmt.Errorf("unsupported workload kind: %s", workload.Kind)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to scale workload %s: %w", workload.Name, err)
		}
	}

	return s, nil
}

// Rollback performs the rollback operation for this state
func (s *ScaleWorkloadState) Rollback(ctx context.Context, data *FailoverContext) error {
	s.log.Info("Rolling back workload scaling")
	return nil
}
