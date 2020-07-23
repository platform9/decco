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

package appcontroller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	kwatch "k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	deccov1beta2 "github.com/platform9/decco/api/v1beta2"
	"github.com/platform9/decco/pkg/app"
	"github.com/platform9/decco/pkg/decco"
	"github.com/platform9/decco/pkg/dns"
	"github.com/platform9/decco/pkg/k8sutil"
	"github.com/platform9/decco/pkg/watcher"
)

type InternalController struct {
	log           *logrus.Entry
	apiHost       string
	extensionsApi apiextensionsclient.Interface
	kubeApi       kubernetes.Interface
	namespace     string
	spaceSpec     deccov1beta2.SpaceSpec
	stopCh        chan interface{}
	dns           dns.Provider
	ingress       decco.Ingress
}

type Controller struct {
	dns       dns.Provider
	ingress   decco.Ingress
	log       *logrus.Entry
	wg        *sync.WaitGroup
	stopCh    chan interface{}
	stopOnce  sync.Once
	spaceSpec deccov1beta2.SpaceSpec
	namespace string
}

// ----------------------------------------------------------------------------

func New(
	log *logrus.Entry,
	namespace string,
	spaceSpec deccov1beta2.SpaceSpec,
	wg *sync.WaitGroup,
	dns dns.Provider,
	ingress decco.Ingress,
) *Controller {
	return &Controller{
		log: log.WithFields(logrus.Fields{
			"namespace": namespace,
			"pkg":       "appcontroller",
		}),
		namespace: namespace,
		wg:        wg,
		spaceSpec: spaceSpec,
		stopCh:    make(chan interface{}),
		dns:       dns,
		ingress:   ingress,
	}
}

// ----------------------------------------------------------------------------

func (ctl *Controller) Start() {
	ctl.wg.Add(1)
	log := ctl.log
	go ctl.shutdownWhenNamespaceGone()
	go func() {
		defer ctl.wg.Done()
		for {
			c := NewInternalController(ctl.namespace, ctl.spaceSpec, ctl.stopCh, ctl.dns, ctl.ingress)
			err := c.Run()
			switch err {
			case watcher.ErrTerminated:
				log.Infof("app controller for %s gracefully terminated",
					ctl.namespace)
				return
			default:
				log.Warnf("restarting internal app controller for %s due to: %v",
					ctl.namespace, err)
				time.Sleep(2 * time.Second)
			}
		}
	}()
}

// ----------------------------------------------------------------------------

func (ctl *Controller) Stop() {
	// It's possible for Stop() to be called multiple times.
	// But a channel can only be closed once, so use this trick from
	// https://groups.google.com/forum/#!topic/golang-nuts/rhxMiNmRAPk
	ctl.stopOnce.Do(func() {
		close(ctl.stopCh)
	})
}

// ----------------------------------------------------------------------------

func NewInternalController(
	namespace string,
	spaceSpec deccov1beta2.SpaceSpec,
	stopCh chan interface{},
	dns dns.Provider,
	ingress decco.Ingress,
) *InternalController {
	clusterConfig := config.GetConfigOrDie()
	logger := logrus.WithFields(logrus.Fields{
		"pkg":       "appcontroller",
		"namespace": namespace,
	})
	logger.Logger.SetLevel(logrus.DebugLevel)
	return &InternalController{
		log:           logger,
		apiHost:       clusterConfig.Host,
		extensionsApi: apiextensionsclient.NewForConfigOrDie(clusterConfig),
		kubeApi:       kubernetes.NewForConfigOrDie(clusterConfig),
		namespace:     namespace,
		spaceSpec:     spaceSpec,
		stopCh:        stopCh,
		dns:           dns,
		ingress:       ingress,
	}
}

// ----------------------------------------------------------------------------

type appWrapper struct {
	app *deccov1beta2.App
}

func (aw *appWrapper) Name() string {
	return aw.app.Name
}

func (ctl *InternalController) GetItemList() (rv string, items []watcher.Item, err error) {
	appList, err := k8sutil.GetAppList(
		ctl.kubeApi.CoreV1().RESTClient(), ctl.namespace)
	if err != nil {
		return
	}
	rv = appList.ResourceVersion
	for i := range appList.Items {
		items = append(items, &appWrapper{&appList.Items[i]})
	}
	return
}

// ----------------------------------------------------------------------------

func (ctl *InternalController) InitItem(item watcher.Item) watcher.ManagedItem {
	wrapped := item.(*appWrapper)
	a := wrapped.app
	a.Spec.Cleanup()
	newApp := app.New(*a, ctl.kubeApi, ctl.namespace, ctl.spaceSpec, ctl.dns, ctl.ingress)
	return newApp
}

// ----------------------------------------------------------------------------

func (c *InternalController) InitCRD() error {
	err := k8sutil.CreateAppCRD(c.extensionsApi)
	if err != nil {
		return err
	}
	return k8sutil.WaitAppCRDReady(c.extensionsApi)
}

// ----------------------------------------------------------------------------

func (c *InternalController) Run() error {
	wl := watcher.CreateWatchLoop(fmt.Sprintf("apps-in-%s",
		c.namespace), c, c.stopCh)
	return wl.Run()
}

// ----------------------------------------------------------------------------

func (c *InternalController) PeriodicTask(itemMap map[string]watcher.ManagedItem) {
	app.Collect(c.kubeApi, c.log, c.namespace, func(name string) bool {
		_, ok := itemMap[name]
		return ok
	})
}

// ----------------------------------------------------------------------------

func (c *InternalController) StartWatchRequest(
	watchVersion string,
) (*http.Response, error) {

	restIf := c.kubeApi.CoreV1().RESTClient()
	httpClnt := restIf.(*rest.RESTClient).Client
	return k8sutil.WatchApps(
		c.apiHost,
		c.namespace,
		httpClnt,
		watchVersion,
	)
}

// ----------------------------------------------------------------------------

func (c *InternalController) LogEvent(evType kwatch.EventType, item watcher.Item) {
	wrapped := item.(*appWrapper)
	a := wrapped.app
	c.log.Debugf("app event: %v %v", evType, a)
}

// ----------------------------------------------------------------------------

func (c *InternalController) UnmarshalItem(
	evType kwatch.EventType,
	data []byte,
) (watcher.Item, string, error) {

	a := &deccov1beta2.App{}
	err := json.Unmarshal(data, a)
	if err != nil {
		s := "failed to unmarshal app object from data (%v): %v"
		c.log.Warnf(s, data, err)
		return nil, "", fmt.Errorf(s, data, err)
	}
	if evType == kwatch.Error {
		c.log.Warnf("UnmarshalItem: kwatch.Error with app: %v", a)
		return nil, a.ResourceVersion, nil
	}
	wrapped := &appWrapper{a}
	return wrapped, a.ResourceVersion, nil
}

// ----------------------------------------------------------------------------

func (c *InternalController) GetItemType() string {
	return "app"
}
