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

// FailoverMode defines the type of failover to perform
// +kubebuilder:validation:Enum=CONSISTENCY;UPTIME
type FailoverMode string

const (
	// FailoverModeConsistency represents a consistency-focused failover that prioritizes data consistency
	FailoverModeConsistency FailoverMode = "CONSISTENCY"
	// FailoverModeUptime represents an uptime-focused failover that prioritizes service availability
	FailoverModeUptime FailoverMode = "UPTIME"
)

// FailoverSpec defines the desired state of Failover
type FailoverSpec struct {
	// TargetCluster specifies which cluster should become the PRIMARY
	// +kubebuilder:validation:Required
	TargetCluster string `json:"targetCluster"`

	// SourceOfTruthCluster specifies which cluster is the source of truth for this resource
	// This cluster's state will be considered authoritative for spec changes
	// +kubebuilder:validation:Required
	SourceOfTruthCluster string `json:"sourceOfTruthCluster"`

	// FailoverMode defines the strategy for failover process
	// CONSISTENCY: Prioritizes data consistency by shutting down source first
	// UPTIME: Prioritizes service uptime by activating target before deactivating source
	// +kubebuilder:validation:Required
	FailoverMode FailoverMode `json:"failoverMode"`

	// When true, skips safety checks like replication lag or volume sync state, and does not roll back automatically. Used in cases of emergency failover.
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

	// Conditions represent the current state of the failover operation
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.status`
//+kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
//+kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.targetCluster`
//+kubebuilder:printcolumn:name="SOT",type=string,JSONPath=`.spec.sourceOfTruthCluster`
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
