// Copyright 2017 The decco Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package appspec

import (
	"encoding/json"
	"errors"

	"k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var (
	ErrInvalidPort                    = errors.New("spec: endpoint has invalid port value")
	ErrInvalidUrlPath                 = errors.New("spec: invalid url path")
	ErrBothUrlPathAndDisableTcpVerify = errors.New("spec: url path and disable tcp verify cannot both be set")
	ErrNoTcpCert                      = errors.New("spec: space does not support TCP apps because cert info missing")
)

// AppList is a list of apps.
type AppList struct {
	metav1.TypeMeta `json:",inline"`
	// Standard list metadata
	// More info: http://releases.k8s.io/HEAD/docs/devel/api-conventions.md#metadata
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []App `json:"items"`
}

func (crl AppList) DeepCopyObject() runtime.Object {
	return &crl
}

type App struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AppSpec   `json:"spec"`
	Status            AppStatus `json:"status"`
}

func (c *App) AsOwner() metav1.OwnerReference {
	trueVar := true
	return metav1.OwnerReference{
		APIVersion: c.APIVersion,
		Kind:       c.Kind,
		Name:       c.Name,
		UID:        c.UID,
		Controller: &trueVar,
	}
}

func (c App) DeepCopyObject() runtime.Object {
	return &c
}

/*
func (c App) GetObjectKind() schema.ObjectKind {
	return &c.TypeMeta
}
*/

// Specifies the stunnel client configuration for connecting to another app
// The following destination specifications are allowed:
// Fqdn not empty : fully qualified domain name for destination
//                  Endpoint and SpaceName ignored.
//                  Example: appname.spacename.svc.cluster.local
// Endpoint not empty, Fqdn and SpaceName empty: connect to the app in the
//                    same namespace. The constructed fqdn is internal and is:
//                    ${Endpoint}.${CURRENT_SPACE_NAME}.svc.cluster.local
// Endpoint and SpaceName not empty, Fqdn empty: connect to the app in the
//                    specified space. The constructed fqdn is internal and is:
//                    ${Endpoint}.${SpaceName}.svc.cluster.local
type TlsEgress struct {
	Fqdn                          string `json:"fqdn"`
	Endpoint                      string `json:"endpoint"`
	SpaceName                     string `json:"spaceName"`
	TargetPort                    int32  `json:"targetPort"`
	LocalPort                     int32  `json:"localPort"` // local listening port
	CertAndCaSecretName           string `json:"certAndCaSecretName"`
	SpringBoardDelaySeconds       int32  `json:"springBoardDelaySeconds"`
	DisableServerCertVerification bool   `json:"disableServerCertVerification"`
}

type AppSpec struct {
	PodSpec                 v1.PodSpec `json:"pod"`
	InitialReplicas         int32      `json:"initialReplicas"`
	Egresses                []TlsEgress
	RunAsJob                bool  `json:"runAsJob"`
	JobBackoffLimit         int32 `json:"jobBackoffLimit"`
	Endpoints               []EndpointSpec
	FirstEndpointListenPort int32
	Permissions             []rbacv1.PolicyRule
	DomainEnvVarName        string `json:"domainEnvVarName"`
}

type EndpointSpec struct {
	Name              string
	Port              int32
	IsMetricsEndpoint bool  // optional, TCP only, no SNI / TLS / stunnel
	TlsListenPort     int32 // optional, defaults to 443 + endpoint index
	// optional Cert and CA if don't want to use default one for the space
	CertAndCaSecretName string `json:"certAndCaSecretName"`
	CreateClearTextSvc  bool   `json:"createClearTextSvc"`

	// The following only apply to http endpoints (httpPath not empty)
	// RewritePath value is interpreted as follows:
	// empty: the path is forwarded unmodified
	// non-empty: the specified HttpPath prefix is replaced with the value
	HttpPath          string `json:"httpPath"`
	RewritePath       string `json:"rewritePath"`
	HttpLocalhostOnly bool   `json:"httpLocalhostOnly"`

	// The following only apply to tcp endpoints (httpPath empty)
	CreateDnsRecord                 bool   `json:"createDnsRecord"`
	DisableTlsTermination           bool   `json:"disableTlsTermination"`
	DisableTcpClientTlsVerification bool   `json:"disableTcpClientTlsVerification"`
	SniHostname                     string `json:"sniHostname"` // optional SNI hostname override
	// Optional name suffix to append to server name for SNI routing purposes.
	// For e.g., if endpoint name is "foo", space domain name is "bar.com",
	// and nameSuffix is ".v0", then the SNI routing name becomes:
	// foo.v0.bar.com
	TcpHostnameSuffix string `json:"tcpHostnameSuffix"`

	// Optional ingress resource annotations
	AdditionalIngressAnnotations map[string]string `json:"additionalIngressAnnotations,omitempty"`
}

// -----------------------------------------------------------------------------

func (c *AppSpec) Validate(tcpCertAndCaSecretName string) error {

	for _, e := range c.Endpoints {
		if e.Port == 0 {
			return ErrInvalidPort
		}
		if e.HttpPath == "" && tcpCertAndCaSecretName == "" {
			return ErrNoTcpCert
		}
		if e.HttpPath != "" && e.DisableTcpClientTlsVerification {
			return ErrBothUrlPathAndDisableTcpVerify
		}
	}
	return nil
}

// Cleanup cleans up user passed spec, e.g. defaulting, transforming fields.
// TODO: move this to admission controller
func (c *AppSpec) Cleanup() {
}

type AppPhase string

const (
	AppPhaseNone     AppPhase = ""
	AppPhaseCreating          = "Creating"
	AppPhaseActive            = "Active"
	AppPhaseFailed            = "Failed"
)

type AppCondition struct {
	Type           AppConditionType `json:"type"`
	Reason         string           `json:"reason"`
	TransitionTime string           `json:"transitionTime"`
}

type AppConditionType string

type AppStatus struct {
	// Phase is the app running phase
	Phase  AppPhase `json:"phase"`
	Reason string   `json:"reason"`
}

func (cs AppStatus) Copy() AppStatus {
	newCRS := AppStatus{}
	b, err := json.Marshal(cs)
	if err != nil {
		panic(err)
	}
	err = json.Unmarshal(b, &newCRS)
	if err != nil {
		panic(err)
	}
	return newCRS
}

func (cs *AppStatus) IsFailed() bool {
	if cs == nil {
		return false
	}
	return cs.Phase == AppPhaseFailed
}

func (cs *AppStatus) SetPhase(p AppPhase) {
	cs.Phase = p
}

func (cs *AppStatus) SetReason(r string) {
	cs.Reason = r
}
