/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FailoverGroupReference defines a reference to a FailoverGroup resource
type FailoverGroupReference struct {
	// Name of the FailoverGroup
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace of the FailoverGroup
	// If not provided, the Failover's namespace will be used
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Status of the failover operation for this group
	// +kubebuilder:validation:Enum=SUCCESS;FAILED;IN_PROGRESS;PENDING
	// +optional
	Status string `json:"status,omitempty"`

	// StartTime is when the failover started for this group
	// +optional
	StartTime string `json:"startTime,omitempty"`

	// CompletionTime is when the failover completed for this group
	// +optional
	CompletionTime string `json:"completionTime,omitempty"`

	// Message provides additional details about the failover operation
	// +optional
	Message string `json:"message,omitempty"`
}

// FailoverMetrics contains metrics related to the failover operation
type FailoverMetrics struct {
	// TotalDowntimeSeconds is the total application downtime during failover
	// +optional
	TotalDowntimeSeconds int64 `json:"totalDowntimeSeconds,omitempty"`

	// TotalFailoverTimeSeconds is the total time taken to complete the failover
	// +optional
	TotalFailoverTimeSeconds int64 `json:"totalFailoverTimeSeconds,omitempty"`
}

// FailoverSpec defines the desired state of Failover
type FailoverSpec struct {
	// TargetCluster specifies which cluster should become the PRIMARY
	// +kubebuilder:validation:Required
	TargetCluster string `json:"targetCluster"`

	// FailoverMode defines the strategy for failover process
	// CONSISTENCY: Prioritizes data consistency by shutting down source first (previously "Safe")
	// UPTIME: Prioritizes service uptime by activating target before deactivating source (previously "Fast")
	// +kubebuilder:validation:Enum=CONSISTENCY;UPTIME
	// +kubebuilder:validation:Required
	FailoverMode string `json:"failoverMode"`

	// When true, skips safety checks like replication lag or volume sync state
	// Use with caution - can lead to data loss if replication isn't complete
	// +optional
	// +kubebuilder:default=false
	Force bool `json:"force,omitempty"`

	// Optional documentation field explaining the reason for this failover
	// Not used by controller logic but useful for auditing and tracking
	// +optional
	Reason string `json:"reason,omitempty"`

	// FailoverGroups references the FailoverGroup resources to be failed over
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	FailoverGroups []FailoverGroupReference `json:"failoverGroups"`
}

// For backward compatibility with existing specs
// +k8s:conversion-gen=false
// +k8s:conversion-gen-external-types=github.com/christensenjairus/Failover-Operator/api/v1alpha1
// This helps API compatibility during transition
type LegacyFailoverSpec struct {
	// When true, overrides component failoverMode settings to use 'fast' mode
	// Fast mode starts the new cluster first before stopping the old one
	// This minimizes downtime but may cause brief data inconsistency
	// +optional
	ForceFastMode bool `json:"forceFastMode,omitempty"`
}

// FailoverStatus defines the observed state of Failover
type FailoverStatus struct {
	// Status of the failover operation
	// +kubebuilder:validation:Enum=SUCCESS;FAILED;IN_PROGRESS
	Status string `json:"status,omitempty"`

	// State reflects the current phase of the failover workflow
	// Values: "INITIALIZING", "SCALING_DOWN_SOURCE", "DEMOTING_VOLUMES", "PROMOTING_VOLUMES", "SCALING_UP_TARGET", "TRANSFERRING_OWNERSHIP", etc.
	// +optional
	State string `json:"state,omitempty"`

	// Message provides additional details about the overall failover operation
	// +optional
	Message string `json:"message,omitempty"`

	// FailoverGroups contains status information for each FailoverGroup
	// +optional
	FailoverGroups []FailoverGroupReference `json:"failoverGroups,omitempty"`

	// Metrics contains metrics related to the failover operation
	// +optional
	Metrics FailoverMetrics `json:"metrics,omitempty"`

	// Conditions represent the current state of the failover operation
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.status`
//+kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
//+kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.targetCluster`
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
//+kubebuilder:validation:XValidation:rule="!has(oldSelf.spec) || oldSelf.spec.targetCluster == self.spec.targetCluster",message="targetCluster is immutable"
//+kubebuilder:validation:XValidation:rule="!has(oldSelf.spec) || oldSelf.spec.failoverMode == self.spec.failoverMode",message="failoverMode is immutable"
//+kubebuilder:validation:XValidation:rule="!has(oldSelf.spec) || !has(oldSelf.spec.force) && !has(self.spec.force) || has(oldSelf.spec.force) && has(self.spec.force) && oldSelf.spec.force == self.spec.force",message="force is immutable"
//+kubebuilder:validation:XValidation:rule="!has(oldSelf.spec) || oldSelf.spec.reason == self.spec.reason",message="reason is immutable"
//+kubebuilder:validation:XValidation:rule="!has(oldSelf.spec) || oldSelf.spec.failoverGroups == self.spec.failoverGroups",message="failoverGroups are immutable"

// Failover is the Schema for the failovers API
type Failover struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FailoverSpec   `json:"spec,omitempty"`
	Status FailoverStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// FailoverList contains a list of Failover
type FailoverList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Failover `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Failover{}, &FailoverList{})
}
