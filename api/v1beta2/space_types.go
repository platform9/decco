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
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ReservedProjectName = "system"
)

type SpacePhase string

const (
	SpacePhaseNone     SpacePhase = ""
	SpacePhaseCreating SpacePhase = "Creating"
	SpacePhaseActive   SpacePhase = "Active"
	SpacePhaseFailed   SpacePhase = "Failed"
	SpacePhaseDeleting SpacePhase = "Deleting"
)

// SpaceSpec defines the desired state of Space
// TODO(erwin) add option to prefix namespaces.
// TODO(erwin) add option deploy kube resources from ConfigMap or URL.
type SpaceSpec struct {

	// DomainName is the base domain used for this space. (required)
	//
	// Decco will use this base domain for any FDQNs registered for
	// Space-specific services. For example: space42.platform9.horse
	DomainName string `json:"domainName"`

	// HttpCertSecretName should point to a TLS secret (in the current
	// namespace) to use for the default-http service. (required)
	//
	// The secret will be copied to the Space's namespace.
	HttpCertSecretName string `json:"httpCertSecretName"`

	// Project can be used to group Spaces.
	Project string `json:"project,omitempty"`

	// TcpCertAndCaSecretName should point to a TLS secret (in the current
	// namespace) to use for stunnel.
	// TODO(erwin) unsure what it is used for. But it is used by kplane
	TcpCertAndCaSecretName string `json:"tcpCertAndCaSecretName,omitempty"`

	// EncryptHttp, if enabled, the ingress will use HTTPS instead of HTTP to
	// communicate with the backend services.
	// TODO(erwin) unsure what it is used for. But it is used by kplane
	EncryptHttp bool `json:"encryptHttp,omitempty"`

	// DeleteHttpCertSecretAfterCopy, if true, signals Decco to delete the
	// original TLS secret specified by HttpCertSecretName after it has been copied.
	DeleteHttpCertSecretAfterCopy bool `json:"deleteHttpCertSecretAfterCopy,omitempty"`

	// DeleteTcpCertAndCaSecretAfterCopy, if true, signals Decco to delete the
	// original TLS secret specified by TcpCertAndCaSecretName after it has been copied.
	DeleteTcpCertAndCaSecretAfterCopy bool `json:"deleteTcpCertAndCaSecretAfterCopy,omitempty"`

	// DisablePrivateIngressController will disable the creation of a
	// Space-specific Ingress controller.
	DisablePrivateIngressController bool `json:"disablePrivateIngressController,omitempty"`

	// VerboseIngressControllerLogging increases log verbosity if set to true.
	VerboseIngressControllerLogging bool `json:"verboseIngressControllerLogging,omitempty"`

	// ?
	PrivateIngressControllerTcpEndpoints []string `json:"privateIngressControllerTcpEndpoints,omitempty"`

	// PrivateIngressControllerTcpHostnameSuffix is an optional suffix to append
	// to host names of private ingress controller's TCP endpoints.
	PrivateIngressControllerTcpHostnameSuffix string `json:"privateIngressControllerTcpHostnameSuffix,omitempty"`

	// CreateDefaultHttpDeploymentAndIngress will create and configure the
	// Space's ingress to include the default-http service.
	CreateDefaultHttpDeploymentAndIngress bool `json:"createDefaultHttpDeploymentAndIngress,omitempty"`

	// ?
	Permissions *SpacePermissions `json:"permissions,omitempty"`
}

// Type: [plain, kustomize]
type Template struct {
	// Type specifies the type of the template. Currently the following options
	// are supported: [plain, kustomize]
	Type string `json:"type"`

	// URL contains the path to the kubernetes resource or resource directory.
	URL string `json:"url"`
	// TODO git repo, ConfigMap string `json:"configMap"`
}

// SpaceStatus defines the observed state of Space
type SpaceStatus struct {
	// Phase indicates the current state of the Space.
	Phase SpacePhase `json:"phase"`

	// Reason is an optional, short, and human-readable explanation for the
	// current phase of the Space.
	Reason string `json:"reason"`

	// Namespace is the Kubernetes namespace associated with this space.
	Namespace string `json:"namespace"`

	// Hostname contains the domain name under which traffic will be redirected
	// to the ingress of the Space.
	Hostname string `json:"hostname"`

	NamespaceProvisioned bool `json:"namespaceProvisioned"`
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
// +kubebuilder:printcolumn:name="Hostname",type="string",JSONPath=".status.hostname"
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
