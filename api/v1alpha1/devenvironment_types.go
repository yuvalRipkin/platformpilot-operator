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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// DevEnvironmentSpec defines the desired state of DevEnvironment
type DevEnvironmentSpec struct {
	// team is the name of the team this environment belongs to, used for RBAC and namespace naming
	// +required
	Team string `json:"team"`

	// envType is the environment type
	// +kubebuilder:validation:Enum=dev;staging
	// +required
	EnvType string `json:"envType"`

	// tier determines resource quota sizing
	// +kubebuilder:validation:Enum=small;medium;large
	// +required
	Tier string `json:"tier"`

	// services is an optional list of services to provision (e.g. postgres, redis)
	// +optional
	Services []string `json:"services,omitempty"`
}

type DevEnvironmentStatus struct {
	// phase is the high-level summary of the environment state
	// +optional
	Phase string `json:"phase,omitempty"`

	// conditions represent the status of individual provisioned resources
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// DevEnvironment is the Schema for the devenvironments API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
type DevEnvironment struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of DevEnvironment
	// +required
	Spec DevEnvironmentSpec `json:"spec"`

	// status defines the observed state of DevEnvironment
	// +optional
	Status DevEnvironmentStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DevEnvironmentList contains a list of DevEnvironment
type DevEnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DevEnvironment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DevEnvironment{}, &DevEnvironmentList{})
}
