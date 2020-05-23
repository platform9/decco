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

	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	deccov1beta2 "github.com/platform9/decco/api/v1beta2"
)

func WatchApps(host string, ns string, httpClient *http.Client, resourceVersion string) (*http.Response, error) {
	return httpClient.Get(fmt.Sprintf("%s/apis/%s/namespaces/%s/%s?watch=true&resourceVersion=%s",
		host, deccov1beta2.GroupVersion.String(), ns, "apps",
		resourceVersion))
}

func GetAppList(restcli rest.Interface,
	ns string) (*deccov1beta2.AppList, error) {
	b, err := restcli.Get().RequestURI(listAppsURI(ns)).DoRaw()
	if err != nil {
		return nil, err
	}

	apps := &deccov1beta2.AppList{}
	if err := json.Unmarshal(b, apps); err != nil {
		return nil, err
	}
	return apps, nil
}

func listAppsURI(ns string) string {
	return fmt.Sprintf("/apis/%s/namespaces/%s/%s",
		deccov1beta2.GroupVersion.String(),
		ns,
		"apps")
}

func UpdateAppCustRsc(
	restcli rest.Interface,
	ns string,
	c deccov1beta2.App,
) (deccov1beta2.App, error) {

	uri := fmt.Sprintf("/apis/%s/namespaces/%s/%s/%s",
		deccov1beta2.GroupVersion.String(),
		ns,
		"apps",
		c.Name)
	b, err := restcli.Put().RequestURI(uri).Body(&c).DoRaw()
	if err != nil {
		return deccov1beta2.App{}, err
	}
	return readAppCR(b)
}

func readAppCR(b []byte) (deccov1beta2.App, error) {
	app := &deccov1beta2.App{}
	if err := json.Unmarshal(b, app); err != nil {
		return deccov1beta2.App{},
			fmt.Errorf("read app CR from json data failed: %v", err)
	}
	return *app, nil
}

func CreateAppCRD(clientset apiextensionsclient.Interface) error {
	crd := &apiextensionsv1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "apps." + deccov1beta2.GroupVersion.Group,
		},
		Spec: apiextensionsv1beta1.CustomResourceDefinitionSpec{
			Group:   deccov1beta2.GroupVersion.Group,
			Version: deccov1beta2.GroupVersion.Version,
			Scope:   apiextensionsv1beta1.NamespaceScoped,
			Names: apiextensionsv1beta1.CustomResourceDefinitionNames{
				Plural:     "apps",
				Kind:       "App",
				ShortNames: []string{"app"},
			},
		},
	}
	_, err := clientset.ApiextensionsV1beta1().CustomResourceDefinitions().Create(crd)
	return err
}

func WaitAppCRDReady(clientset apiextensionsclient.Interface) error {
	err := retryutil.Retry(5*time.Second, 20, func() (bool, error) {
		crd, err := clientset.ApiextensionsV1beta1().
			CustomResourceDefinitions().Get("app", metav1.GetOptions{})
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
