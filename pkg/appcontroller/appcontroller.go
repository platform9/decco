// Copyright 2017 The decco Authors
// Copyright 2016 The etcd-operator Authors
//
// This file was adapted from
// https://github.com/coreos/etcd-operator/blob/master/pkg/controller/controller.go
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
	"github.com/sirupsen/logrus"
	"github.com/platform9/decco/pkg/k8sutil"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	kwatch "k8s.io/apimachinery/pkg/watch"
	"fmt"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"github.com/platform9/decco/pkg/app"
	"time"
	"net/http"
	"github.com/platform9/decco/pkg/spec"
	"github.com/platform9/decco/pkg/appspec"
	"github.com/platform9/decco/pkg/watcher"
	"sync"
)

type Event struct {
	Type   kwatch.EventType
	Object *appspec.App
}

type InternalController struct {
	log           *logrus.Entry
	apiHost       string
	extensionsApi apiextensionsclient.Interface
	kubeApi       kubernetes.Interface
	appInfo       map[string] *app.AppRuntime
	namespace     string
	spaceSpec     spec.SpaceSpec
	stopCh        chan interface{}
}

type Controller struct {
	log           *logrus.Entry
	wg            *sync.WaitGroup
	stopCh        chan interface{}
	stopOnce      sync.Once
	spaceSpec     spec.SpaceSpec
	namespace     string
}

// ----------------------------------------------------------------------------

func New(
	log *logrus.Entry,
	namespace string,
	spaceSpec spec.SpaceSpec,
	wg *sync.WaitGroup,
) *Controller {
	return &Controller{
		log:        log.WithFields(logrus.Fields{
			"namespace": namespace,
			"pkg": "appcontroller",
		}),
		namespace:  namespace,
		wg:         wg,
		spaceSpec:  spaceSpec,
		stopCh:     make(chan interface{}),
	}
}

// ----------------------------------------------------------------------------

func (ctl *Controller) Start() {
	ctl.wg.Add(1)
	log := ctl.log
	go ctl.shutdownWhenNamespaceGone()
	go func () {
		defer ctl.wg.Done()
		for {
			c := NewInternalController(ctl.namespace, ctl.spaceSpec, ctl.stopCh)
			err := c.Run()
			switch err {
			case watcher.ErrTerminated:
				log.Infof("app controller for %s gracefully terminated",
					ctl.namespace)
				return
			default:
				log.Warnf("restarting app controller for %s due to: %v",
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
	spaceSpec spec.SpaceSpec,
	stopCh chan interface{},
) *InternalController {
	clustConfig := k8sutil.GetClusterConfigOrDie()
	logger := logrus.WithFields(logrus.Fields{
		"pkg": "appcontroller",
		"namespace": namespace,
	})
	logger.Logger.SetLevel(logrus.DebugLevel)
	return &InternalController{
		log:           logger,
		apiHost:       clustConfig.Host,
		extensionsApi: k8sutil.MustNewKubeExtClient(),
		kubeApi:       kubernetes.NewForConfigOrDie(clustConfig),
		appInfo:       make(map[string] *app.AppRuntime),
		namespace:     namespace,
		spaceSpec:     spaceSpec,
		stopCh:        stopCh,
	}
}

// ----------------------------------------------------------------------------

func (ctl *InternalController) reconcileApps() (string, error) {
	ctl.log.Info("reconciling apps ...")
	appList, err := k8sutil.GetAppList(
		ctl.kubeApi.CoreV1().RESTClient(), ctl.namespace)
	if err != nil {
		return "", err
	}

	m := make(map[string]bool)
	ctl.log.Debug("--- app reconciliation begin ---")
	for _, a := range appList.Items {
		m[a.Name] = true
	}
	for name, a := range ctl.appInfo {
		appName := a.GetApp().Name
		if name != appName {
			return "", fmt.Errorf("name mismatch: %s vs %s", name, appName)
		}
		if !m[name] {
			ctl.log.Infof("deleting app %s during reconciliation", name)
			a.Delete()
			delete(ctl.appInfo, name)
		}
	}

	for _, a := range appList.Items {
		_, present := ctl.appInfo[a.Name]
		if !present {
			a.Spec.Cleanup()
			newApp, err := app.New(a, ctl.kubeApi, ctl.namespace, ctl.spaceSpec)
			if err != nil {
				ctl.log.Warnf("app runtime creation failed: %s", err)
				continue
			}
			ctl.appInfo[a.Name] = newApp
		}
	}
	ctl.log.Debug("--- app reconciliation end ---")
	return appList.ResourceVersion, nil
}

// ----------------------------------------------------------------------------

func (c *InternalController) initCRD() error {
	err := k8sutil.CreateAppCRD(c.extensionsApi)
	if err != nil {
		return err
	}
	return k8sutil.WaitAppCRDReady(c.extensionsApi)
}

// ----------------------------------------------------------------------------

func (c *InternalController) Resync() (string, error) {
	watchVersion := "0"
	err := c.initCRD()
	if err != nil {
		if k8sutil.IsKubernetesResourceAlreadyExistError(err) {
			// CRD has been initialized before. We need to recover existing apps.
			watchVersion, err = c.reconcileApps()
			if err != nil {
				return "", err
			}
		} else {
			return "", fmt.Errorf("fail to create CRD: %v", err)
		}
	}
	return watchVersion, nil
}

// ----------------------------------------------------------------------------

func (c *InternalController) Run() error {
	wl := watcher.CreateWatchLoop(fmt.Sprintf("apps-in-%s",
		c.namespace), c, c.stopCh)
	return wl.Run()
}

// ----------------------------------------------------------------------------

func (c *InternalController) PeriodicTask() {
	knownUrlPaths := map[string] bool {}
	for _, info := range c.appInfo {
		urlPath := info.GetApp().Spec.HttpUrlPath
		if len(urlPath) > 0 {
			knownUrlPaths[urlPath] = true
		}
	}

	app.Collect(c.kubeApi, c.log, c.namespace, func(name string) bool {
		_, ok := c.appInfo[name]
		return ok
	}, func(urlPath string) bool {
		return knownUrlPaths[urlPath]
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


func (c *InternalController) HandleEvent(e interface{}) (objVersion string, err error) {
	event := e.(*Event)
	c.log.Debugf("app event: %v %v", event.Type, event.Object)
	return c.handleAppEvent(event)
}

// ----------------------------------------------------------------------------

func (c *InternalController) handleAppEvent(event *Event) (objVersion string, err error) {
	a := event.Object
	objVersion = event.Object.ResourceVersion

	// TODO: add validation to appspec update.
	a.Spec.Cleanup()

	switch event.Type {
	case kwatch.Added:
		if _, ok := c.appInfo[a.Name]; ok {
			err = fmt.Errorf("unsafe state. a (%s) was created" +
				" before but we received event (%s)", a.Name, event.Type)
			return
		}

		var newApp *app.AppRuntime
		newApp, err = app.New(*a, c.kubeApi, c.namespace, c.spaceSpec)
		if err != nil {
			c.log.Warnf("app runtime creation failed: %s", err)
			return
		}
		c.appInfo[a.Name] = newApp
		c.log.Printf("app (%s) added. There are now %d apps",
			a.Name, len(c.appInfo))

	case kwatch.Modified:
		if _, ok := c.appInfo[a.Name]; !ok {
			err = fmt.Errorf("unsafe state. a (%s) was never" +
				" created but we received event (%s)", a.Name, event.Type)
			return
		}
		c.appInfo[a.Name].Update(*a)
		c.log.Printf("app (%s) modified. There are now %d apps",
			a.Name, len(c.appInfo))

	case kwatch.Deleted:
		if _, ok := c.appInfo[a.Name]; !ok {
			err = fmt.Errorf("unsafe state. a (%s) was never " +
				"created but we received event (%s)", a.Name, event.Type)
			return
		}
		c.appInfo[a.Name].Delete()
		delete(c.appInfo, a.Name)
		c.log.Printf("app (%s) deleted. There are now %d apps",
			a.Name, len(c.appInfo))
	}
	return
}

// ----------------------------------------------------------------------------

func (c *InternalController) UnmarshalEvent(
	evType kwatch.EventType,
	data []byte,
) (interface{}, error) {

	ev := &Event{
		Type:   evType,
		Object: &appspec.App{},
	}
	err := json.Unmarshal(data, ev.Object)
	if err != nil {
		return nil, fmt.Errorf("fail to unmarshal app object from data (%s): %v", data, err)
	}
	return ev, nil
}

