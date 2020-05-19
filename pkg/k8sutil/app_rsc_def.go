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

	spec "github.com/platform9/decco/pkg/appspec"

	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

func WatchApps(host string, ns string, httpClient *http.Client, resourceVersion string) (*http.Response, error) {
	return httpClient.Get(fmt.Sprintf("%s/apis/%s/namespaces/%s/%s?watch=true&resourceVersion=%s",
		host, spec.SchemeGroupVersion.String(), ns, spec.CRDResourcePlural, resourceVersion))
}

func GetAppList(restcli rest.Interface,
	ns string) (*spec.AppList, error) {
	b, err := restcli.Get().RequestURI(listAppsURI(ns)).DoRaw()
	if err != nil {
		return nil, err
	}

	apps := &spec.AppList{}
	if err := json.Unmarshal(b, apps); err != nil {
		return nil, err
	}
	return apps, nil
}

func listAppsURI(ns string) string {
	return fmt.Sprintf("/apis/%s/namespaces/%s/%s",
		spec.SchemeGroupVersion.String(),
		ns,
		spec.CRDResourcePlural)
}

func UpdateAppCustRsc(
	restcli rest.Interface,
	ns string,
	c spec.App,
) (spec.App, error) {

	uri := fmt.Sprintf("/apis/%s/namespaces/%s/%s/%s",
		spec.SchemeGroupVersion.String(),
		ns,
		spec.CRDResourcePlural,
		c.Name)
	b, err := restcli.Put().RequestURI(uri).Body(&c).DoRaw()
	if err != nil {
		return spec.App{}, err
	}
	return readAppCR(b)
}

func readAppCR(b []byte) (spec.App, error) {
	app := &spec.App{}
	if err := json.Unmarshal(b, app); err != nil {
		return spec.App{},
			fmt.Errorf("read app CR from json data failed: %v", err)
	}
	return *app, nil
}

func CreateAppCRD(clientset apiextensionsclient.Interface) error {
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

func WaitAppCRDReady(clientset apiextensionsclient.Interface) error {
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
