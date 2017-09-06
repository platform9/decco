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
)

var (
	ErrDomainNameMissing = errors.New("spec: missing domain name")
	ErrCertSecretNameMissing = errors.New("spec: missing certificate secret name")
	ErrCaSecretNameMissing = errors.New("spec: missing CA secret name")
)

// CustomerRegionList is a list of customerregions.
type CustomerRegionList struct {
	metav1.TypeMeta `json:",inline"`
	// Standard list metadata
	// More info: http://releases.k8s.io/HEAD/docs/devel/api-conventions.md#metadata
	metav1.ListMeta                  `json:"metadata,omitempty"`
	Items           []CustomerRegion `json:"items"`
}

func (crl CustomerRegionList) DeepCopyObject() runtime.Object {
	return &crl
}

type CustomerRegion struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              CustomerRegionSpec   `json:"spec"`
	Status            CustomerRegionStatus `json:"status"`
}

func (c *CustomerRegion) AsOwner() metav1.OwnerReference {
	trueVar := true
	return metav1.OwnerReference{
		APIVersion: c.APIVersion,
		Kind:       c.Kind,
		Name:       c.Name,
		UID:        c.UID,
		Controller: &trueVar,
	}
}

func (c CustomerRegion) DeepCopyObject() runtime.Object {
	return &c
}

type CustomerRegionSpec struct {
	DomainName string `json:"domainName"`
	CertSecretName string `json:"certSecretName"`
	CertAuthoritySecretName string `json:"certAuthoritySecretName"`
}

func (c *CustomerRegionSpec) Validate() error {
	if c.DomainName == "" {
		return ErrDomainNameMissing
	}
	if c.CertSecretName == "" {
		return ErrCertSecretNameMissing
	}
	if c.CertAuthoritySecretName == "" {
		return ErrCaSecretNameMissing
	}
	return nil
}

// Cleanup cleans up user passed spec, e.g. defaulting, transforming fields.
// TODO: move this to admission controller
func (c *CustomerRegionSpec) Cleanup() {
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

type CustomerRegionPhase string

const (
	CustomerRegionPhaseNone     CustomerRegionPhase = ""
	CustomerRegionPhaseCreating              = "Creating"
	CustomerRegionPhaseRunning               = "Running"
	CustomerRegionPhaseFailed                = "Failed"
)

type CustomerRegionCondition struct {
	Type CustomerRegionConditionType `json:"type"`
	Reason string `json:"reason"`
	TransitionTime string `json:"transitionTime"`
}

type CustomerRegionConditionType string

type CustomerRegionStatus struct {
	// Phase is the CustomerRegion running phase
	Phase  CustomerRegionPhase `json:"phase"`
	Reason string       `json:"reason"`
}

func (cs CustomerRegionStatus) Copy() CustomerRegionStatus {
	newCRS := CustomerRegionStatus{}
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

func (cs *CustomerRegionStatus) IsFailed() bool {
	if cs == nil {
		return false
	}
	return cs.Phase == CustomerRegionPhaseFailed
}

func (cs *CustomerRegionStatus) SetPhase(p CustomerRegionPhase) {
	cs.Phase = p
}

func (cs *CustomerRegionStatus) SetReason(r string) {
	cs.Reason = r
}
