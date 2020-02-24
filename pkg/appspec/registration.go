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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

const (
	CRDResourceKind   = "App"
	CRDResourcePlural = "apps"
	CRDShortName      = "app"
	groupName         = "decco.platform9.com"
)

var (
	SchemeBuilder      = runtime.NewSchemeBuilder(addKnownTypes)
	Scheme             = runtime.NewScheme()
	Codecs             = serializer.NewCodecFactory(Scheme)
	SchemeGroupVersion = schema.GroupVersion{Group: groupName, Version: "v1beta2"}
	CRDName            = CRDResourcePlural + "." + groupName
)

// addKnownTypes adds the set of types defined in this package to the supplied scheme.
func addKnownTypes(s *runtime.Scheme) error {
	a := App{}
	al := AppList{}
	s.AddKnownTypes(SchemeGroupVersion, &a, &al)
	metav1.AddToGroupVersion(s, SchemeGroupVersion)
	return nil
}

func init() {
	utilruntime.Must(SchemeBuilder.AddToScheme(Scheme))
}
