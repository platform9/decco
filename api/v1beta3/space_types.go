/*

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

package v1beta3

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SpaceSpec defines the desired state of Space
type SpaceSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +optional
	ManualPhase string `json:"manualPhase,omitempty"`

	// Manifest is a resource composed of Apps
	Manifest Manifest `json:"manifest"`
}

// SpaceStatus defines the observed state of Space
type SpaceStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Phase represents the current phase of Space creation.
	// E.g. Pending, Active, Terminating, Failed etc.
	// +optional
	Phase string `json:"phase,omitempty"`
}

// SetTypedPhase sets the Phase field to the string representation of SpacePhase.
func (s *SpaceStatus) SetTypedPhase(p SpacePhase) {
	s.Phase = string(p)
}

// GetTypedPhase attempts to parse the Phase field and return
// the typed SpacePhase representation as described in `space_phase_types.go`.
func (s *SpaceStatus) GetTypedPhase() SpacePhase {
	switch phase := SpacePhase(s.Phase); phase {
	case
		SpacePhasePending,
		SpacePhaseProvisioning,
		SpacePhaseProvisioned,
		SpacePhaseActive,
		SpacePhaseDeleting,
		SpacePhaseDeleted,
		SpacePhaseFailed:
		return phase
	default:
		return SpacePhaseUnknown
	}
}

// https://github.com/kubernetes-sigs/kubebuilder/issues/751#issuecomment-498485515 for below comment
// since it doesn't happen by default with the scaffolding

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Space is the Schema for the spaces API
type Space struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SpaceSpec   `json:"spec,omitempty"`
	Status SpaceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SpaceList contains a list of Space
type SpaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Space `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Space{}, &SpaceList{})
}
