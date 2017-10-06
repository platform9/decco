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
)

const (
)

var (
	ErrContainerMissing = errors.New("spec: missing container")
	ErrContainerInvalidPorts = errors.New("spec: container must declare exactly one port")
	ErrInvalidUrlPath = errors.New("spec: invalid url path")
	ErrBothUrlPathAndVerifyTcp = errors.New("spec: url path and verify tcp cannot both be set")
	ErrNoTcpCert = errors.New("spec: customer region does not support TCP apps because cert info missing")
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

type AppSpec struct {
	HttpUrlPath string `json:"httpUrlPath"`
	VerifyTcpClientCert bool `json:"verifyTcpClientCert"`
	ContainerSpec v1.Container `json:"container"`
	InitialReplicas int32 `json:"initialReplicas"`
}

func (c *AppSpec) Validate(tcpCertAndCaSecretName string) error {
	if c.ContainerSpec.Name == "" {
		return ErrContainerMissing
	}
	if len(c.ContainerSpec.Ports) != 1 {
		return ErrContainerInvalidPorts
	}
	if c.HttpUrlPath == "/" {
		return ErrInvalidUrlPath
	}
	if c.HttpUrlPath == "" && tcpCertAndCaSecretName == "" {
		return ErrNoTcpCert
	}
	if c.HttpUrlPath != "" && c.VerifyTcpClientCert {
		return ErrBothUrlPathAndVerifyTcp
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
	// Phase is the App running phase
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

