// Copyright 2016 The etcd-operator Authors
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

package k8sutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/coreos/etcd-operator/pkg/util/retryutil"

	"github.com/platform9/decco/pkg/spec"

	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

func WatchSpaces(host string, httpClient *http.Client, resourceVersion string) (*http.Response, error) {
	return httpClient.Get(fmt.Sprintf("%s/apis/%s/%s?watch=true&resourceVersion=%s",
		host, spec.SchemeGroupVersion.String(), spec.CRDResourcePlural, resourceVersion))
}

func GetSpaceList(restcli rest.Interface) (*spec.SpaceList, error) {
	b, err := restcli.Get().RequestURI(listSpacesURI()).DoRaw()
	if err != nil {
		return nil, err
	}

	spaces := &spec.SpaceList{}
	if err := json.Unmarshal(b, spaces); err != nil {
		return nil, err
	}
	return spaces, nil
}

func listSpacesURI() string {
	return fmt.Sprintf("/apis/%s/%s",
		spec.SchemeGroupVersion.String(),
		spec.CRDResourcePlural)
}

func UpdateSpaceCustRsc(
	restcli rest.Interface,
	c spec.Space,
) (spec.Space, error) {
	uri := fmt.Sprintf("/apis/%s/namespaces/%s/%s/%s",
		spec.SchemeGroupVersion.String(),
		c.Namespace,
		spec.CRDResourcePlural,
		c.Name)
	b, err := restcli.Put().RequestURI(uri).Body(&c).DoRaw()
	if err != nil {
		return spec.Space{}, err
	}
	return readSpaceCR(b)
}

func readSpaceCR(b []byte) (spec.Space, error) {
	space := &spec.Space{}
	if err := json.Unmarshal(b, space); err != nil {
		return spec.Space{},
			fmt.Errorf("read space CR from json data failed: %v", err)
	}
	return *space, nil
}

func CreateCRD(clientset apiextensionsclient.Interface) error {
	crd := &apiextensionsv1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: spec.CRDName,
		},
		Spec: apiextensionsv1beta1.CustomResourceDefinitionSpec{
			Group:   spec.SchemeGroupVersion.Group,
			Version: spec.SchemeGroupVersion.Version,
			Scope:   apiextensionsv1beta1.NamespaceScoped,
			Names: apiextensionsv1beta1.CustomResourceDefinitionNames{
				Plural:     spec.CRDResourcePlural,
				Kind:       spec.CRDResourceKind,
				ShortNames: []string{spec.CRDShortName},
			},
		},
	}
	_, err := clientset.ApiextensionsV1beta1().CustomResourceDefinitions().Create(crd)
	return err
}

func WaitCRDReady(clientset apiextensionsclient.Interface) error {
	err := retryutil.Retry(5*time.Second, 20, func() (bool, error) {
		crd, err := clientset.ApiextensionsV1beta1().CustomResourceDefinitions().Get(spec.CRDName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		for _, cond := range crd.Status.Conditions {
			switch cond.Type {
			case apiextensionsv1beta1.Established:
				if cond.Status == apiextensionsv1beta1.ConditionTrue {
					return true, nil
				}
			case apiextensionsv1beta1.NamesAccepted:
				if cond.Status == apiextensionsv1beta1.ConditionFalse {
					return false, fmt.Errorf("Name conflict: %v", cond.Reason)
				}
			}
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("wait CRD created failed: %v", err)
	}
	return nil
}

func MustNewKubeExtClient() apiextensionsclient.Interface {
	cfg := GetClusterConfigOrDie()
	return apiextensionsclient.NewForConfigOrDie(cfg)
}
