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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"fmt"
)

const (
)

var (
	ErrNoEndpoints             = errors.New("spec: has no endpoints")
	ErrInvalidPort             = errors.New("spec: endpoint has invalid port value")
	ErrInvalidUrlPath          = errors.New("spec: invalid url path")
	ErrBothUrlPathAndDisableTcpVerify = errors.New("spec: url path and disable tcp verify cannot both be set")
	ErrNoTcpCert               = errors.New("spec: space does not support TCP apps because cert info missing")
)

// AppList is a list of apps.
type AppList struct {
	metav1.TypeMeta `json:",inline"`
	// Standard list metadata
	// More info: http://releases.k8s.io/HEAD/docs/devel/api-conventions.md#metadata
	metav1.ListMeta                  `json:"metadata,omitempty"`
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
	Fqdn string                `json:"fqdn"`
	Endpoint string            `json:"endpoint"`
	SpaceName string           `json:"spaceName"`
	TargetPort int32           `json:"targetPort"`
	LocalPort int32            `json:"localPort"`   // local listening port
	CertAndCaSecretName string `json:"certAndCaSecretName"`
	SpringBoardDelaySeconds int32 `json:"springBoardDelaySeconds"`
	DisableServerCertVerification bool `json:"disableServerCertVerification"`
}

type AppSpec struct {
	PodSpec         v1.PodSpec `json:"pod"`
	InitialReplicas int32 `json:"initialReplicas"`
	Egresses        []TlsEgress
	RunAsJob        bool `json:"runAsJob"`
	Endpoints       []EndpointSpec
	LogFiles        []LogFileSpec
}

type LogFileSpec struct {
	ContainerName string `json:"containerName"`
	Path string `json:"path"`
	Tag string `json:"tag"`
}

type EndpointSpec struct {
	Name                string
	Port                int32
	// optional Cert and CA if don't want to use default one for the space
	CertAndCaSecretName string `json:"certAndCaSecretName"`
	CreateClearTextSvc  bool   `json:"createClearTextSvc"`
	// The following only apply to http endpoints (httpPath not empty)
	// RewritePath value is interpreted as follows:
	// empty: the path is forwarded unmodified
	// non-empty: the specified HttpPath prefix is replaced with the value
	HttpPath             string `json:"httpPath"`
	RewritePath          string `json:"rewritePath"`
	// The following only apply to tcp endpoints (httpPath empty)
	CreateDnsRecord     bool   `json:"createDnsRecord"`
	DisableTcpClientTlsVerification bool   `json:"disableTcpClientTlsVerification"`
}

func (c *AppSpec) Validate(tcpCertAndCaSecretName string) error {

	if !c.RunAsJob && len(c.Endpoints) < 1 {
		return ErrNoEndpoints
	}
	for _, e := range c.Endpoints {
		if e.Port == 0 {
			return ErrInvalidPort
		}
		if e.HttpPath == "/" {
			return ErrInvalidUrlPath
		}
		if e.HttpPath == "" && tcpCertAndCaSecretName == "" {
			return ErrNoTcpCert
		}
		if e.HttpPath != "" && e.DisableTcpClientTlsVerification {
			return ErrBothUrlPathAndDisableTcpVerify
		}
	}
	for _, logFile := range c.LogFiles {
		found := false
		cname := logFile.ContainerName
		for _, c := range c.PodSpec.Containers {
			if c.Name == cname {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("logfile references non-existent container: %s",
				cname)
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
	AppPhaseCreating                     = "Creating"
	AppPhaseActive                       = "Active"
	AppPhaseFailed                       = "Failed"
)

type AppCondition struct {
	Type AppConditionType `json:"type"`
	Reason string `json:"reason"`
	TransitionTime string `json:"transitionTime"`
}

type AppConditionType string

type AppStatus struct {
	// Phase is the app running phase
	Phase  AppPhase `json:"phase"`
	Reason string       `json:"reason"`
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

