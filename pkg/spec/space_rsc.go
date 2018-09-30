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

package spec

import (
	"encoding/json"
	"errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	RESERVED_PROJECT_NAME = "system"
)

var (
	ErrDomainNameMissing = errors.New("spec: missing domain name")
	ErrHttpCertSecretNameMissing = errors.New("spec: missing certificate secret name")
	ErrInvalidProjectName = errors.New("spec: invalid project name")
)

// SpaceList is a list of spaces.
type SpaceList struct {
	metav1.TypeMeta `json:",inline"`
	// Standard list metadata
	// More info: http://releases.k8s.io/HEAD/docs/devel/api-conventions.md#metadata
	metav1.ListMeta                  `json:"metadata,omitempty"`
	Items           []Space `json:"items"`
}

func (crl SpaceList) DeepCopyObject() runtime.Object {
	return &crl
}

type Space struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              SpaceSpec   `json:"spec"`
	Status            SpaceStatus `json:"status"`
}

func (c *Space) AsOwner() metav1.OwnerReference {
	trueVar := true
	return metav1.OwnerReference{
		APIVersion: c.APIVersion,
		Kind:       c.Kind,
		Name:       c.Name,
		UID:        c.UID,
		Controller: &trueVar,
	}
}

func (c Space) DeepCopyObject() runtime.Object {
	return &c
}

type SpaceSpec struct {
	DomainName             string `json:"domainName"`
	Project                string `json:"project"`
	HttpCertSecretName     string `json:"httpCertSecretName"`
	TcpCertAndCaSecretName string `json:"tcpCertAndCaSecretName"`
	EncryptHttp            bool   `json:"encryptHttp"`
	DeleteHttpCertSecretAfterCopy bool   `json:"deleteHttpCertSecretAfterCopy"`
	DeleteTcpCertAndCaSecretAfterCopy bool   `json:"deleteTcpCertAndCaSecretAfterCopy"`
	DisablePrivateIngressController bool `json:"disablePrivateIngressController"`
	VerboseIngressControllerLogging bool `json:"verboseIngressControllerLogging"`
	PrivateIngressControllerTcpEndpoints []string `json:"privateIngressControllerTcpEndpoints"`
}

func (c *SpaceSpec) Validate() error {
	if c.DomainName == "" {
		return ErrDomainNameMissing
	}
	if c.HttpCertSecretName == "" {
		return ErrHttpCertSecretNameMissing
	}
	if c.Project == RESERVED_PROJECT_NAME {
		return ErrInvalidProjectName
	}
	return nil
}

// Cleanup cleans up user passed spec, e.g. defaulting, transforming fields.
// TODO: move this to admission controller
func (c *SpaceSpec) Cleanup() {
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

type SpacePhase string

const (
	SpacePhaseNone     SpacePhase = ""
	SpacePhaseCreating                     = "Creating"
	SpacePhaseActive                       = "Active"
	SpacePhaseFailed                       = "Failed"
)

type SpaceCondition struct {
	Type SpaceConditionType `json:"type"`
	Reason string `json:"reason"`
	TransitionTime string `json:"transitionTime"`
}

type SpaceConditionType string

type SpaceStatus struct {
	// Phase is the Space running phase
	Phase  SpacePhase `json:"phase"`
	Reason string       `json:"reason"`
}

func (cs SpaceStatus) Copy() SpaceStatus {
	newCRS := SpaceStatus{}
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

func (cs *SpaceStatus) IsFailed() bool {
	if cs == nil {
		return false
	}
	return cs.Phase == SpacePhaseFailed
}

func (cs *SpaceStatus) SetPhase(p SpacePhase) {
	cs.Phase = p
}

func (cs *SpaceStatus) SetReason(r string) {
	cs.Reason = r
}

