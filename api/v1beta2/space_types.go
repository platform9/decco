/*
Copyright 2017-2020 The decco Authors

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

package v1beta2

import (
	"errors"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ReservedProjectName = "system"
)

var (
	ErrDomainNameMissing         = errors.New("spec: missing domain name")
	ErrHttpCertSecretNameMissing = errors.New("spec: missing certificate secret name")
	ErrInvalidProjectName        = errors.New("spec: invalid project name")
)

type SpacePhase string

type SpaceCondition struct {
	Type           SpaceConditionType `json:"type"`
	Reason         string             `json:"reason"`
	TransitionTime string             `json:"transitionTime"`
}

type SpaceConditionType string

const (
	SpacePhaseNone     SpacePhase = ""
	SpacePhaseCreating            = "Creating"
	SpacePhaseActive              = "Active"
	SpacePhaseFailed              = "Failed"
	SpacePhaseDeleting            = "Deleting"
)

// SpaceSpec defines the desired state of Space
type SpaceSpec struct {
	DomainName                           string   `json:"domainName"`
	Project                              string   `json:"project,omitempty"`
	HttpCertSecretName                   string   `json:"httpCertSecretName"`
	TcpCertAndCaSecretName               string   `json:"tcpCertAndCaSecretName,omitempty"`
	EncryptHttp                          bool     `json:"encryptHttp,omitempty"`
	DeleteHttpCertSecretAfterCopy        bool     `json:"deleteHttpCertSecretAfterCopy,omitempty"`
	DeleteTcpCertAndCaSecretAfterCopy    bool     `json:"deleteTcpCertAndCaSecretAfterCopy,omitempty"`
	DisablePrivateIngressController      bool     `json:"disablePrivateIngressController,omitempty"`
	VerboseIngressControllerLogging      bool     `json:"verboseIngressControllerLogging,omitempty"`
	PrivateIngressControllerTcpEndpoints []string `json:"privateIngressControllerTcpEndpoints,omitempty"`
	// Optional suffix to append to host names of private ingress controller's tcp endpoints
	PrivateIngressControllerTcpHostnameSuffix string            `json:"privateIngressControllerTcpHostnameSuffix,omitempty"`
	Permissions                               *SpacePermissions `json:"permissions,omitempty"`
	CreateDefaultHttpDeploymentAndIngress     bool              `json:"createDefaultHttpDeploymentAndIngress,omitempty"`
}

func (in *SpaceSpec) Validate() error {
	if in.DomainName == "" {
		return ErrDomainNameMissing
	}
	if in.HttpCertSecretName == "" {
		return ErrHttpCertSecretNameMissing
	}
	if in.Project == ReservedProjectName {
		return ErrInvalidProjectName
	}
	return nil
}

// Cleanup cleans up user passed spec, e.g. defaulting, transforming fields.
// TODO: move this to admission controller
func (in *SpaceSpec) Cleanup() {
	/*
		if len(c.BaseImage) == 0 {
			c.BaseImage = defaultBaseImage
		}

		if len(c.Version) == 0 {
			c.Version = defaultVersion
		}

		c.Version = strings.TrimLeft(c.Version, "v")
	*/
}

// SpaceStatus defines the observed state of Space
type SpaceStatus struct {
	Phase         SpacePhase `json:"phase"`
	Reason        string     `json:"reason"`
	Namespace     string     `json:"namespace"`
	DNSConfigured bool       `json:"dnsConfigured"`
}

func (in *SpaceStatus) IsFailed() bool {
	if in == nil {
		return false
	}
	return in.Phase == SpacePhaseFailed
}

func (in *SpaceStatus) SetPhase(phase SpacePhase, reason string) {
	in.Phase = phase
	in.Reason = reason
}

// +kubebuilder:object:root=true
// +kubebuilder:printcolumn:name="Domain",type="string",JSONPath=".spec.domainName"
// +kubebuilder:printcolumn:name="Namespace",type="string",JSONPath=".status.namespace"
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// Space is the Schema for the spaces API
type Space struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SpaceSpec   `json:"spec,omitempty"`
	Status SpaceStatus `json:"status,omitempty"`
}

func (in *Space) AsOwner() metav1.OwnerReference {
	trueVar := true
	return metav1.OwnerReference{
		APIVersion: in.APIVersion,
		Kind:       in.Kind,
		Name:       in.Name,
		UID:        in.UID,
		Controller: &trueVar,
	}
}

// +kubebuilder:object:root=true

// SpaceList contains a list of Space
type SpaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Space `json:"items"`
}

type SpacePermissions struct {
	Subject rbacv1.Subject      `json:"subject"`
	Rules   []rbacv1.PolicyRule `json:"rules"`
}

func init() {
	SchemeBuilder.Register(&Space{}, &SpaceList{})
}
