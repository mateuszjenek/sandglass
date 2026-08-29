/*
Copyright 2026.

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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// RoutingSpec defines the ingress routing configuration for the EphemeralDeployment.
type RoutingSpec struct {
	// HeaderName is the HTTP header key used for matching. Defaults to "X-Sandglass".
	// +optional
	// +kubebuilder:default="X-Sandglass"
	HeaderName string `json:"headerName,omitempty"`

	// HeaderMatch is the value for the HTTP header that will trigger routing.
	// If omitted, it defaults to the EphemeralDeployment metadata.name.
	// +optional
	// +kubebuilder:validation:MaxLength=63
	HeaderMatch string `json:"headerMatch,omitempty"`

	// GatewayRef is the name of the Gateway resource in the same namespace that
	// the generated HTTPRoute will attach to. Defaults to "main-gateway".
	// +optional
	// +kubebuilder:default="main-gateway"
	GatewayRef string `json:"gatewayRef,omitempty"`

	// ServicePort specifies the explicit port number or name on the service to route traffic to.
	// If omitted, Sandglass auto-detects the target port from the baseline service.
	// +optional
	ServicePort *intstr.IntOrString `json:"servicePort,omitempty"`
}

// EphemeralDeploymentSpec defines the desired state of EphemeralDeployment
type EphemeralDeploymentSpec struct {
	// TargetDeployment is the name of the baseline Deployment to clone.
	// +kubebuilder:validation:Required
	TargetDeployment string `json:"targetDeployment"`

	// Routing defines the Gateway API routing parameters for this ephemeral environment.
	// +optional
	Routing *RoutingSpec `json:"routing,omitempty"`

	// Replicas defines the desired number of pods for the ephemeral preview environment. Defaults to 1.
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	Replicas *int32 `json:"replicas,omitempty"`

	// TTL defines how long the ephemeral environment should live after creation.
	// +optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern="^([0-9]+(h|m|s|ms|us|ns|d))+$"
	TTL *metav1.Duration `json:"ttl,omitempty"`

	// PodPatch is a partial PodTemplateSpec that will be merged with the baseline deployment's template.
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	PodPatch *corev1.PodTemplateSpec `json:"podPatch,omitempty"`
}

// EphemeralDeploymentStatus defines the observed state of EphemeralDeployment.
type EphemeralDeploymentStatus struct {
	// Phase represents the current phase of the ephemeral service (e.g., Pending, Ready, Failed).
	// +optional
	Phase string `json:"phase,omitempty"`

	// Active indicates whether the ephemeral environment is fully active.
	// +optional
	Active bool `json:"active,omitempty"`

	// ExpiresAt indicates when this ephemeral service will be automatically deleted.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// Conditions represent the current state of the EphemeralDeployment resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.targetDeployment`
// +kubebuilder:printcolumn:name="HeaderKey",type=string,JSONPath=`.spec.routing.headerName`
// +kubebuilder:printcolumn:name="HeaderValue",type=string,JSONPath=`.spec.routing.headerMatch`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Active",type=boolean,JSONPath=`.status.active`
// +kubebuilder:printcolumn:name="ExpiresAt",type=string,JSONPath=`.status.expiresAt`,priority=1

// EphemeralDeployment is the Schema for the ephemeraldeployments API
type EphemeralDeployment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EphemeralDeploymentSpec   `json:"spec"`
	Status EphemeralDeploymentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// EphemeralDeploymentList contains a list of EphemeralDeployment
type EphemeralDeploymentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EphemeralDeployment `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &EphemeralDeployment{}, &EphemeralDeploymentList{})
		return nil
	})
}
