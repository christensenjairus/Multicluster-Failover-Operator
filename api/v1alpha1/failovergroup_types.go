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

// TimeoutSettings defines configurable timeouts for failover operations
type TimeoutSettings struct {
	// Maximum time a FailoverGroup can remain in FAILOVER/FAILBACK states
	// After this period, the operator initiates an automatic rollback
	// +optional
	TransitoryState string `json:"transitoryState,omitempty"`

	// Time that a PRIMARY cluster can remain unhealthy before auto-failover
	// The operator creates a failover after this period if health status=ERROR
	// +optional
	UnhealthyPrimary string `json:"unhealthyPrimary,omitempty"`

	// Time without heartbeats before assuming a cluster is down
	// The operator creates a failover after this period if no heartbeats are received
	// +optional
	Heartbeat string `json:"heartbeat,omitempty"`
}

// ClusterInfo contains information about a cluster in the FailoverGroup
type ClusterInfo struct {
	// Name of the cluster
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Role of the cluster (PRIMARY or STANDBY)
	// +kubebuilder:validation:Enum=PRIMARY;STANDBY
	// +optional
	Role string `json:"role,omitempty"`

	// Health status of the cluster
	// +kubebuilder:validation:Enum=OK;DEGRADED;ERROR;UNKNOWN
	// +optional
	Health string `json:"health,omitempty"`

	// LastHeartbeat is the timestamp of the last heartbeat received
	// +optional
	LastHeartbeat string `json:"lastHeartbeat,omitempty"`
}

// GlobalStateInfo contains global state information synced from DynamoDB
type GlobalStateInfo struct {
	// Which cluster is currently PRIMARY for this group
	// +optional
	ActiveCluster string `json:"activeCluster,omitempty"`

	// Reference to the most recent failover operation
	// +optional
	LastFailover map[string]string `json:"lastFailover,omitempty"`

	// LastSyncTime is when the last successful sync with DynamoDB occurred
	// +optional
	LastSyncTime string `json:"lastSyncTime,omitempty"`

	// Information about all clusters participating in this FailoverGroup
	// +optional
	Clusters []ClusterInfo `json:"clusters,omitempty"`
}

// VolumeReplication defines a volume replication specification
type VolumeReplicationSpec struct {
	// Name of the PVC or volume claim
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

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

	// Documentation field explaining why automatic failovers are suspended
	// Only meaningful when suspended=true
	// +optional
	SuspensionReason string `json:"suspensionReason,omitempty"`

	// Timeout settings for automatic operations
	// +optional
	Timeouts TimeoutSettings `json:"timeouts,omitempty"`

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

// WorkloadStatus defines the status of a workload
type WorkloadStatus struct {
	// Kind of the workload resource
	Kind string `json:"kind"`

	// Name of the workload resource
	Name string `json:"name"`

	// Health indicates the health status of the workload
	// Values: "OK", "DEGRADED", "ERROR"
	// +kubebuilder:validation:Enum=OK;DEGRADED;ERROR
	Health string `json:"health"`

	// Status provides additional details about the workload status
	// +optional
	Status string `json:"status,omitempty"`

	// VolumeReplications contains the status of volume replications associated with this workload
	// +optional
	VolumeReplications []VolumeReplicationStatus `json:"volumeReplications,omitempty"`
}

// VolumeReplicationStatus defines the status of a volume replication
type VolumeReplicationStatus struct {
	// Name of the volume replication
	Name string `json:"name"`

	// Health indicates the replication health status
	// Values: "OK", "DEGRADED", "ERROR"
	// +kubebuilder:validation:Enum=OK;DEGRADED;ERROR
	Health string `json:"health"`

	// Status provides additional details about the replication status
	// +optional
	Status string `json:"status,omitempty"`
}

// NetworkResourceStatus defines the status of a network resource
type NetworkResourceStatus struct {
	// Kind of the network resource
	Kind string `json:"kind"`

	// Name of the network resource
	Name string `json:"name"`

	// Health indicates the health status of the network resource
	// Values: "OK", "DEGRADED", "ERROR"
	// +kubebuilder:validation:Enum=OK;DEGRADED;ERROR
	Health string `json:"health"`

	// Status provides additional details about the network resource status
	// +optional
	Status string `json:"status,omitempty"`
}

// FluxResourceStatus defines the status of a Flux GitOps resource
type FluxResourceStatus struct {
	// Kind of the Flux resource
	Kind string `json:"kind"`

	// Name of the Flux resource
	Name string `json:"name"`

	// Health indicates the health status of the Flux resource
	// Values: "OK", "DEGRADED", "ERROR"
	// +kubebuilder:validation:Enum=OK;DEGRADED;ERROR
	Health string `json:"health"`

	// Status provides additional details about the Flux resource status
	// +optional
	Status string `json:"status,omitempty"`
}

// FailoverGroupStatus defines the observed state of FailoverGroup
type FailoverGroupStatus struct {
	// Health indicates the overall health of the failover group
	// Values: "OK", "DEGRADED", "ERROR"
	// +kubebuilder:validation:Enum=OK;DEGRADED;ERROR
	Health string `json:"health,omitempty"`

	// Suspended indicates if the failover group is currently suspended
	// This directly reflects the spec.suspended field and is shown in status for easier visibility
	Suspended bool `json:"suspended"`

	// Workloads contains status information for each workload defined in the spec
	// +optional
	Workloads []WorkloadStatus `json:"workloads,omitempty"`

	// NetworkResources contains status information for each network resource defined in the spec
	// +optional
	NetworkResources []NetworkResourceStatus `json:"networkResources,omitempty"`

	// FluxResources contains status information for each Flux resource defined in the spec
	// +optional
	FluxResources []FluxResourceStatus `json:"fluxResources,omitempty"`

	// LastFailoverTime is the time when the last failover operation completed
	// +optional
	LastFailoverTime string `json:"lastFailoverTime,omitempty"`

	// GlobalState contains global state information synced from DynamoDB
	// +optional
	GlobalState GlobalStateInfo `json:"globalState,omitempty"`

	// Conditions represent the current state of failover reconciliation.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Health",type=string,JSONPath=`.status.health`
//+kubebuilder:printcolumn:name="Suspended",type=boolean,JSONPath=`.status.suspended`
//+kubebuilder:printcolumn:name="Active",type=string,JSONPath=`.status.globalState.activeCluster`
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
