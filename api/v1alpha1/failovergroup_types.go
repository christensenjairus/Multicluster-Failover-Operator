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

// WorkloadSpec defines a workload resource
type WorkloadSpec struct {
	// Kind of the workload resource
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Deployment;StatefulSet;CronJob
	Kind string `json:"kind"`

	// Name of the workload resource
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// VolumeReplications are the volume replications associated with this workload
	// +optional
	VolumeReplications []string `json:"volumeReplications,omitempty"`
}

// NetworkResourceSpec defines a network resource
type NetworkResourceSpec struct {
	// Kind of the network resource
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=VirtualService;Ingress
	Kind string `json:"kind"`

	// Name of the network resource
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// FluxResourceSpec defines a Flux GitOps resource
type FluxResourceSpec struct {
	// Kind of the Flux resource
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=HelmRelease;Kustomization
	Kind string `json:"kind"`

	// Name of the Flux resource
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Whether to trigger reconciliation of this resource during failover
	// +optional
	TriggerReconcile bool `json:"triggerReconcile,omitempty"`
}

// FailoverGroupSpec defines the desired state of FailoverGroup
type FailoverGroupSpec struct {
	// Identifier for the operator instance that should process this FailoverGroup
	// This allows running multiple operator instances for different applications
	// +optional
	OperatorID string `json:"operatorID,omitempty"`

	// When true, automatic failovers are disabled (manual override for maintenance)
	// The operator will not create automatic failovers even if it detects problems
	// +kubebuilder:default=false
	Suspended bool `json:"suspended"`

	// How often the operator updates heartbeats in DynamoDB
	// This controls the frequency of cluster health updates in the global state
	// +optional
	HeartbeatInterval string `json:"heartbeatInterval,omitempty"`

	// Workloads that need scaling and health tracking during failover
	// +optional
	Workloads []WorkloadSpec `json:"workloads,omitempty"`

	// Network resources that just need annotation flips during failover
	// +optional
	NetworkResources []NetworkResourceSpec `json:"networkResources,omitempty"`

	// Flux resources to manage during failover
	// +optional
	FluxResources []FluxResourceSpec `json:"fluxResources,omitempty"`
}

// FailoverGroupStatus defines the observed state of FailoverGroup
type FailoverGroupStatus struct {
	// Which cluster is currently PRIMARY for this group
	// +optional
	ActiveCluster string `json:"activeCluster,omitempty"`

	// Reference to the most recent failover operation
	// +optional
	LastFailover map[string]string `json:"lastFailover,omitempty"`

	// Health status of the active cluster
	// +kubebuilder:validation:Enum=OK;DEGRADED;ERROR;UNKNOWN
	// +optional
	Health string `json:"health,omitempty"`

	// LastHeartbeat is the timestamp of the last heartbeat received
	// +optional
	LastHeartbeat string `json:"lastHeartbeat,omitempty"`

	// Conditions represent the current state of failover reconciliation.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Health",type=string,JSONPath=`.status.health`
//+kubebuilder:printcolumn:name="Suspended",type=boolean,JSONPath=`.spec.suspended`
//+kubebuilder:printcolumn:name="Active Cluster",type=string,JSONPath=`.status.activeCluster`
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// FailoverGroup is the Schema for the failovergroups API
type FailoverGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FailoverGroupSpec   `json:"spec,omitempty"`
	Status FailoverGroupStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// FailoverGroupList contains a list of FailoverGroup
type FailoverGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FailoverGroup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FailoverGroup{}, &FailoverGroupList{})
}
