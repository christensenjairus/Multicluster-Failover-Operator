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

// ResourceRef defines a simple reference to a Kubernetes resource by kind and name
type ResourceRef struct {
	// Kind of the resource
	// +kubebuilder:validation:Required
	Kind string `json:"kind"`

	// Name of the resource
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// ComponentSpec defines a component in the application being managed
type ComponentSpec struct {
	// Name of the component
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// FailoverMode determines the failover approach for this specific component
	// If not specified, the default failover mode from the parent will be used
	// +kubebuilder:validation:Enum=safe;fast
	// +optional
	FailoverMode string `json:"failoverMode,omitempty"`

	// Workloads are the resources that make up this component
	// +optional
	Workloads []ResourceRef `json:"workloads,omitempty"`

	// VolumeReplications are the volume replications associated with this component
	// +optional
	VolumeReplications []string `json:"volumeReplications,omitempty"`

	// VirtualServices are the Istio virtual services associated with this component
	// +optional
	VirtualServices []string `json:"virtualServices,omitempty"`

	// Ingresses are the Kubernetes ingress resources associated with this component
	// +optional
	Ingresses []string `json:"ingresses,omitempty"`
}

// ComponentStatus contains status information for a component
type ComponentStatus struct {
	// Name of the component
	Name string `json:"name"`

	// Health indicates the health status of the component
	// Values: "OK", "DEGRADED", "ERROR"
	// +kubebuilder:validation:Enum=OK;DEGRADED;ERROR
	Health string `json:"health"`

	// Message provides additional details about the component health
	// +optional
	Message string `json:"message,omitempty"`
}

// SupportedResourceKind represents a supported resource kind and its associated API group
type SupportedResourceKind struct {
	// Kind is the resource kind
	Kind string
	// APIGroup is the resource API group (without version)
	APIGroup string
	// DefaultAPIGroup is used when APIGroup is omitted
	DefaultAPIGroup string
}

// Define supported resource kinds as constants
// Note: These constants help maintain a single source of truth for supported resources
var (
	// VolumeReplicationKind represents VolumeReplication resources
	VolumeReplicationKind = SupportedResourceKind{
		Kind:            "VolumeReplication",
		APIGroup:        "replication.storage.openshift.io",
		DefaultAPIGroup: "replication.storage.openshift.io",
	}

	// DeploymentKind represents Deployment resources
	DeploymentKind = SupportedResourceKind{
		Kind:            "Deployment",
		APIGroup:        "apps",
		DefaultAPIGroup: "apps",
	}

	// StatefulSetKind represents StatefulSet resources
	StatefulSetKind = SupportedResourceKind{
		Kind:            "StatefulSet",
		APIGroup:        "apps",
		DefaultAPIGroup: "apps",
	}

	// CronJobKind represents CronJob resources
	CronJobKind = SupportedResourceKind{
		Kind:            "CronJob",
		APIGroup:        "batch",
		DefaultAPIGroup: "batch",
	}

	// VirtualServiceKind represents VirtualService resources
	VirtualServiceKind = SupportedResourceKind{
		Kind:            "VirtualService",
		APIGroup:        "networking.istio.io",
		DefaultAPIGroup: "networking.istio.io",
	}

	// HelmReleaseKind represents HelmRelease resources
	HelmReleaseKind = SupportedResourceKind{
		Kind:            "HelmRelease",
		APIGroup:        "helm.toolkit.fluxcd.io",
		DefaultAPIGroup: "helm.toolkit.fluxcd.io",
	}

	// KustomizationKind represents Kustomization resources
	KustomizationKind = SupportedResourceKind{
		Kind:            "Kustomization",
		APIGroup:        "kustomize.toolkit.fluxcd.io",
		DefaultAPIGroup: "kustomize.toolkit.fluxcd.io",
	}

	// IngressKind represents Ingress resources
	IngressKind = SupportedResourceKind{
		Kind:            "Ingress",
		APIGroup:        "networking.k8s.io",
		DefaultAPIGroup: "networking.k8s.io",
	}
)

// AllSupportedResourceKinds defines all resource kinds supported by the operator
var AllSupportedResourceKinds = []SupportedResourceKind{
	VolumeReplicationKind,
	DeploymentKind,
	StatefulSetKind,
	CronJobKind,
	VirtualServiceKind,
	HelmReleaseKind,
	KustomizationKind,
	IngressKind,
}

// GetSupportedKinds returns a list of all supported resource kinds
func GetSupportedKinds() []string {
	kinds := make([]string, len(AllSupportedResourceKinds))
	for i, k := range AllSupportedResourceKinds {
		kinds[i] = k.Kind
	}
	return kinds
}

// GetSupportedAPIGroups returns a list of all supported API groups
func GetSupportedAPIGroups() []string {
	groups := make([]string, len(AllSupportedResourceKinds))
	for i, k := range AllSupportedResourceKinds {
		groups[i] = k.APIGroup
	}
	return groups
}

// FailoverGroupState represents the possible states of a FailoverGroup
type FailoverGroupState string

const (
	// FailoverGroupStatePrimary represents the PRIMARY state
	// This is the active state where workloads are running and serving traffic
	FailoverGroupStatePrimary FailoverGroupState = "PRIMARY"

	// FailoverGroupStateStandby represents the STANDBY state
	// This is the inactive state where workloads are scaled down
	FailoverGroupStateStandby FailoverGroupState = "STANDBY"

	// FailoverGroupStateInProgress represents the IN_PROGRESS state
	// This is a general transitionary state during failover operations
	FailoverGroupStateInProgress FailoverGroupState = "IN_PROGRESS"

	// FailoverGroupStateFailover represents the transitory state when a cluster
	// is transitioning from STANDBY to PRIMARY (becoming active)
	FailoverGroupStateFailover FailoverGroupState = "FAILOVER"

	// FailoverGroupStateFailback represents the transitory state when a cluster
	// is transitioning from PRIMARY to STANDBY (becoming inactive)
	FailoverGroupStateFailback FailoverGroupState = "FAILBACK"
)
