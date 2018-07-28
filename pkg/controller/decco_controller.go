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
	"net/http"
	"github.com/platform9/decco/pkg/spec"
	"github.com/platform9/decco/pkg/appcontroller"
	"github.com/platform9/decco/pkg/watcher"
	"k8s.io/client-go/rest"
	"sync"
)

func init() {
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
	logrus.Println("controller package initialized")
}

type Controller struct {
	log *logrus.Entry
	apiHost string
	extensionsApi apiextensionsclient.Interface
	kubeApi kubernetes.Interface
	namespace string
	waitApps sync.WaitGroup
}

type SpaceInfo struct {
	spc *space.SpaceRuntime
	appCtrl    *appcontroller.Controller
	log *logrus.Entry
}

// ----------------------------------------------------------------------------

func (spcInfo *SpaceInfo) Name() string {
	return spcInfo.spc.Space.Name
}

// ----------------------------------------------------------------------------

func (spcInfo *SpaceInfo) Delete()  {
	nsDeleted := spcInfo.spc.Delete()
	if !nsDeleted {
		// An active space resource might have no associated namespace if for
		// example, a user manually deleted the namespace while decco is not
		// running. Call Detach() to explicitly stop the app controller.
		spcInfo.log.Debugf("ns not deleted: forcing stop of app ctrl")
		spcInfo.Stop()
	}
}

// ----------------------------------------------------------------------------

func (spcInfo *SpaceInfo) Stop() {
	if spcInfo.appCtrl != nil {
		// shut down the app controller. A new app controller instance will
		// start as a child of the future decco controller instance
		spcInfo.appCtrl.Stop()
	} else {
		spcInfo.log.Debugf("Detach: no app controller to stop")
	}
}

// ----------------------------------------------------------------------------

func (spcInfo *SpaceInfo) Update(item watcher.Item)  {
	wrapped := item.(*spaceWrapper)
	spc := wrapped.space
	spcInfo.spc.Update(*spc)
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
		namespace: namespace,
	}
}

// ----------------------------------------------------------------------------

func (c *Controller) InitCRD() error {
	err := k8sutil.CreateCRD(c.extensionsApi)
	if err != nil {
		return err
	}
	return k8sutil.WaitCRDReady(c.extensionsApi)
}

// ----------------------------------------------------------------------------

func (c *Controller) GetItemType() string {
	return "space"
}

// ----------------------------------------------------------------------------

func (c *Controller) Run() error {
	defer func() {
		c.log.Infof("waiting for app controllers to shut down ...")
		c.waitApps.Wait()
		c.log.Infof("all app controllers have shut down.")
	}()

	wl := watcher.CreateWatchLoop(fmt.Sprintf("spaces-in-%s",
		c.namespace), c, make(chan interface{}))
	return wl.Run()
}

// ----------------------------------------------------------------------------

func (c *Controller) PeriodicTask(itemMap map[string]watcher.ManagedItem) {
	space.Collect(c.kubeApi, c.log, func(name string) bool {
		_, ok := itemMap[name]
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

func (c *Controller) InitItem(item watcher.Item) watcher.ManagedItem {
	wrapped := item.(*spaceWrapper)
	spc := wrapped.space
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
	return &SpaceInfo{
		spc: newSpace,
		appCtrl: appCtrl,
		log: c.log.WithField("spaceInfo", spc.Name),
	}
}

// ----------------------------------------------------------------------------

func (c *Controller) LogEvent(evType kwatch.EventType, item watcher.Item) {
	wrapped := item.(*spaceWrapper)
	spc := wrapped.space
	c.log.Debugf("space event: %v %v", evType, spc)
}

// ----------------------------------------------------------------------------

func (c *Controller) UnmarshalItem(
	evType kwatch.EventType,
	data []byte,
) (watcher.Item, string, error) {

	spc := &spec.Space{}
	err := json.Unmarshal(data, spc)
	if err != nil {
		return nil, "", fmt.Errorf("fail to unmarshal space object from data (%s): %v", data, err)
	}
	wrapped := &spaceWrapper{spc}
	return wrapped, spc.ResourceVersion, nil
}

// ----------------------------------------------------------------------------

type spaceWrapper struct {
	space *spec.Space
}

func (sw *spaceWrapper) Name() string {
	return sw.space.Name
}

// ----------------------------------------------------------------------------

func (ctl *Controller) GetItemList() (rv string, items []watcher.Item, err error) {
	spcList, err := k8sutil.GetSpaceList(
		ctl.kubeApi.CoreV1().RESTClient(), ctl.namespace)
	if err != nil {
		return
	}
	rv = spcList.ResourceVersion
	for i, _ := range spcList.Items {
		items = append(items, &spaceWrapper{space: &spcList.Items[i]})
	}
	return
}