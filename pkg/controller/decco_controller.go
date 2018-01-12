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

package controller

import (
	"encoding/json"
	"github.com/sirupsen/logrus"
	"github.com/platform9/decco/pkg/k8sutil"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	kwatch "k8s.io/apimachinery/pkg/watch"
	"fmt"
	"k8s.io/client-go/kubernetes"
	"github.com/platform9/decco/pkg/space"
	"time"
	"net/http"
	"github.com/platform9/decco/pkg/spec"
	"github.com/platform9/decco/pkg/appcontroller"
	"github.com/platform9/decco/pkg/watcher"
	"k8s.io/client-go/rest"
	"sync"
)

var (
	initRetryWaitTime = 30 * time.Second
	delayAfterWatchStreamCloseInSeconds = 2
)


func init() {
	logrus.Println("controller package initialized")
}

type Event struct {
	Type   kwatch.EventType
	Object *spec.Space
}

type Controller struct {
	log *logrus.Entry
	apiHost string
	extensionsApi apiextensionsclient.Interface
	kubeApi kubernetes.Interface
	spcInfo map[string] SpaceInfo
	namespace string
	waitApps sync.WaitGroup
}

type SpaceInfo struct {
	spc *space.SpaceRuntime
	appCtrl    *appcontroller.Controller
}

// ----------------------------------------------------------------------------

func New(namespace string) *Controller {
	clustConfig := k8sutil.GetClusterConfigOrDie()
	logger := logrus.WithField("pkg", "controller")
	logger.Logger.SetLevel(logrus.DebugLevel)
	return &Controller{
		log: logger,
		apiHost: clustConfig.Host,
		extensionsApi: k8sutil.MustNewKubeExtClient(),
		kubeApi: kubernetes.NewForConfigOrDie(clustConfig),
		spcInfo: make(map[string] SpaceInfo),
		namespace: namespace,
	}
}

// ----------------------------------------------------------------------------

func (ctl *Controller) reconcileSpaces() (string, error) {
	log := ctl.log.WithField("func", "reconcileSpaces")
	log.Info("reconciling spaces...")
	spcList, err := k8sutil.GetSpaceList(
		ctl.kubeApi.CoreV1().RESTClient(), ctl.namespace)
	if err != nil {
		return "", err
	}

	m := make(map[string]bool)
	log.Debug("--- space reconciliation begin ---")
	for _, spc := range spcList.Items {
		m[spc.Name] = true
	}
	for name, spc := range ctl.spcInfo {
		if name != spc.spc.Space.Name {
			return "", fmt.Errorf("name mismatch: %s vs %s", name,
				spc.spc.Space.Name)
		}
		if !m[name] {
			log.Infof("deleting space %s during reconciliation", name)
			ctl.unregisterSpace(name, true)
		}
	}
	for _, spc := range spcList.Items {
		_, present := ctl.spcInfo[spc.Name]
		if !present {
			spc.Spec.Cleanup()
			ctl.registerSpace(&spc)
		}
	}
	log.Debug("--- space reconciliation end ---")
	return spcList.ResourceVersion, nil
}

// ----------------------------------------------------------------------------

func (c *Controller) initCRD() error {
	err := k8sutil.CreateCRD(c.extensionsApi)
	if err != nil {
		return err
	}
	return k8sutil.WaitCRDReady(c.extensionsApi)
}

// ----------------------------------------------------------------------------

func (c *Controller) Resync() (string, error) {
	watchVersion := "0"
	err := c.initCRD()
	if err != nil {
		if k8sutil.IsKubernetesResourceAlreadyExistError(err) {
			// CRD has been initialized before. We need to recover existing spaces.
			watchVersion, err = c.reconcileSpaces()
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

func (c *Controller) Run() error {
	defer func() {
		for key, _ := range c.spcInfo {
			c.unregisterSpace(key, false)
		}
		c.log.Infof("waiting for app controllers to shut down ...")
		c.waitApps.Wait()
		c.log.Infof("all app controllers have shut down.")
	}()

	wl := watcher.CreateWatchLoop(fmt.Sprintf("spaces-in-%s",
		c.namespace), c, make(chan interface{}))
	return wl.Run()
}

// ----------------------------------------------------------------------------

func (c *Controller) PeriodicTask() {
	space.Collect(c.kubeApi, c.log, func(name string) bool {
		_, ok := c.spcInfo[name]
		return ok
	})
}

// ----------------------------------------------------------------------------

func (c *Controller) StartWatchRequest(watchVersion string) (*http.Response, error) {
	restIf := c.kubeApi.CoreV1().RESTClient()
	httpClient := restIf.(*rest.RESTClient).Client
	return k8sutil.WatchSpaces(
		c.apiHost,
		c.namespace,
		httpClient,
		watchVersion,
	)
}

// ----------------------------------------------------------------------------

func (c *Controller) unregisterSpace(
	name string,
	deleteResources bool,
) {
	if spcInfo, ok := c.spcInfo[name]; ok {
		c.log.Debugf("unregistering space %s", name)
		delete(c.spcInfo, name)
		if deleteResources {
			spcInfo.spc.Delete()
			// the app controller will eventually detect the deletion
			// of the associated namespace resource and shut itself down
		} else if spcInfo.appCtrl != nil {
			// shut down the app controller. A new app controller instance will
			// start as a child of the future decco controller instance
			spcInfo.appCtrl.Stop()
		}
	}
}

// ----------------------------------------------------------------------------

func (c *Controller) registerSpace(spc *spec.Space) {
	newSpace := space.New(*spc, c.kubeApi, c.namespace)
	var appCtrl *appcontroller.Controller
	if newSpace.Status.Phase == spec.SpacePhaseActive {
		c.log.Infof("starting app controller for %s", spc.Name)
		appCtrl = appcontroller.New(
			c.log, spc.Name,
			spc.Spec, &c.waitApps,
		)
		appCtrl.Start()
	} else {
		c.log.Warnf("not starting app controller for failed space %s",
			spc.Name)
	}
	c.spcInfo[spc.Name] = SpaceInfo{
		spc: newSpace,
		appCtrl: appCtrl,
	}
}

// ----------------------------------------------------------------------------

func (c *Controller) HandleEvent(e interface{}) (objVersion string, err error) {
	event := e.(*Event)
	c.log.Debugf("space event: %v %v", event.Type, event.Object)
	return c.handleSpaceEvent(event)
}

// ----------------------------------------------------------------------------

func (c *Controller) handleSpaceEvent(event *Event) (objVersion string, err error) {
	spc := event.Object
	objVersion = event.Object.ResourceVersion
	// TODO: add validation to spec update.
	spc.Spec.Cleanup()

	switch event.Type {
	case kwatch.Added:
		if _, ok := c.spcInfo[spc.Name]; ok {
			err = fmt.Errorf("unsafe state. space (%s) was registered" +
				" before but we received event (%s)", spc.Name, event.Type)
			return
		}
		c.registerSpace(spc)
		c.log.Printf("space (%s) added. " +
			"There now %d spaces", spc.Name, len(c.spcInfo))

	case kwatch.Modified:
		if _, ok := c.spcInfo[spc.Name]; !ok {
			err = fmt.Errorf("unsafe state. space (%s) was not" +
				" registered but we received event (%s)", spc.Name, event.Type)
			return
		}
		c.spcInfo[spc.Name].spc.Update(*spc)
		c.log.Printf("space (%s) modified. " +
			"There now %d spaces", spc.Name, len(c.spcInfo))

	case kwatch.Deleted:
		if _, ok := c.spcInfo[spc.Name]; !ok {
			err = fmt.Errorf("unsafe state. space (%s) was not " +
				"registered but we received event (%s)", spc.Name, event.Type)
			return
		}
		c.unregisterSpace(spc.Name, true)
		c.log.Printf("space (%s) deleted. " +
			"There now %d spaces", spc.Name, len(c.spcInfo))
	}
	return
}

// ----------------------------------------------------------------------------

func (c *Controller) UnmarshalEvent(
	evType kwatch.EventType,
	data []byte,
) (interface{}, error) {

	ev := &Event{
		Type:   evType,
		Object: &spec.Space{},
	}
	err := json.Unmarshal(data, ev.Object)
	if err != nil {
		return nil, fmt.Errorf("fail to unmarshal Space object from data (%s): %v", data, err)
	}
	return ev, nil
}
