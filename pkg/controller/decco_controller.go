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
	"io"
	"errors"
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
	"github.com/platform9/decco/pkg/client"
	"github.com/platform9/decco/pkg/appcontroller"
	"sync"
)

var (
	initRetryWaitTime = 30 * time.Second
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
	rscVersion *string
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

func (ctl *Controller) findAllSpaces() (string, error) {
	ctl.log.Info("finding existing spaces...")
	spcList, err := k8sutil.GetSpaceList(
		ctl.kubeApi.CoreV1().RESTClient(), ctl.namespace)
	if err != nil {
		return "", err
	}

	for i := range spcList.Items {
		spc := spcList.Items[i]
		spc.Spec.Cleanup()
		ctl.registerSpace(&spc)
	}

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

func (c *Controller) initResource() (string, error) {
	watchVersion := "0"
	err := c.initCRD()
	if err != nil {
		if k8sutil.IsKubernetesResourceAlreadyExistError(err) {
			// CRD has been initialized before. We need to recover existing spaces.
			watchVersion, err = c.findAllSpaces()
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
	var (
		watchVersion string
		err          error
	)

	restConfig := k8sutil.GetClusterConfigOrDie()
	restClnt, _, err := client.New(restConfig)
	if err != nil {
		return err
	}
	for {
		watchVersion, err = c.initResource()
		if err == nil {
			break
		}
		c.log.Errorf("initialization failed: %v", err)
		c.log.Infof("retry in %v...", initRetryWaitTime)
		time.Sleep(initRetryWaitTime)
		// todo: add max retry?
	}

	defer func() {
		for key, _ := range c.spcInfo {
			c.unregisterSpace(key, false)
		}
		c.log.Infof("waiting for app controllers to shut down ...")
		c.waitApps.Wait()
		c.log.Infof("all app controllers have shut down.")
	}()

	c.log.Infof("controller started in namespace %s " +
		"with %d space runtimes and initial watch version: %s",
		c.namespace, len(c.spcInfo), watchVersion)
	err = c.watch(watchVersion, restClnt.Client, c.collectGarbage)
	return err
}

// ----------------------------------------------------------------------------

func (c *Controller) collectGarbage() {
	space.Collect(c.kubeApi, c.log, func(name string) bool {
		_, ok := c.spcInfo[name]
		return ok
	})
}

// ----------------------------------------------------------------------------

func (c *Controller) watch(
	watchVersion string,
	httpClient *http.Client,
	periodicCallback func(),
) error {

	for {
		periodicCallback()
		resp, err := k8sutil.WatchSpaces(
			c.apiHost,
			c.namespace,
			httpClient,
			watchVersion,
		)
		c.log.Infof("start watching at %v", watchVersion)

		if err != nil {
			return err
		}

		watchVersion, err = c.processWatchResponse(watchVersion, resp)
		if err != nil {
			return err
		}
	}
}

// ----------------------------------------------------------------------------

func (c *Controller) processWatchResponse(
	initialWatchVersion string,
	resp *http.Response) (
		nextWatchVersion string,
		err error) {

	nextWatchVersion = initialWatchVersion
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("invalid status code: " + resp.Status)
	}

	decoder := json.NewDecoder(resp.Body)
	for {
		ev, st, err := pollEvent(decoder)
		if err != nil {
			if err == io.EOF { // apiserver will close stream periodically
				c.log.Info("apiserver closed watch stream, retrying after 2s...")
				time.Sleep(2 * time.Second)
				return nextWatchVersion, nil
			}
			c.log.Errorf("received invalid event from watch API: %v", err)
			return "", err
		}

		if st != nil {
			err = fmt.Errorf("unexpected watch error: %v", st)
			return "", err
		}

		c.log.Debugf("space event: %v %v",
			ev.Type,
			ev.Object,
		)

		logrus.Infof("next watch version: %s", nextWatchVersion)
		isDelete, err := c.handleSpaceEvent(ev)
		if err != nil {
			c.log.Warningf("event handler returned possible error: %v", err)
			return "", err
		}
		if !isDelete {
			nextWatchVersion = ev.Object.ResourceVersion
		}
	}
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
	initialRV := spc.ResourceVersion
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
		rscVersion: &initialRV,
		appCtrl: appCtrl,
	}
}

// ----------------------------------------------------------------------------

func (c *Controller) handleSpaceEvent(event *Event) (isDelete bool, err error) {
	spc := event.Object
	// TODO: add validation to spec update.
	spc.Spec.Cleanup()

	switch event.Type {
	case kwatch.Added:
		if _, ok := c.spcInfo[spc.Name]; ok {
			return false, fmt.Errorf("unsafe state. space (%s) was registered" +
				" before but we received event (%s)", spc.Name, event.Type)
		}
		c.registerSpace(spc)
		c.log.Printf("space (%s) added. " +
			"There now %d spaces", spc.Name, len(c.spcInfo))

	case kwatch.Modified:
		if _, ok := c.spcInfo[spc.Name]; !ok {
			return false, fmt.Errorf("unsafe state. space (%s) was not" +
				" registered but we received event (%s)", spc.Name, event.Type)
		}
		c.spcInfo[spc.Name].spc.Update(*spc)
		*(c.spcInfo[spc.Name].rscVersion) = spc.ResourceVersion
		c.log.Printf("space (%s) modified. " +
			"There now %d spaces", spc.Name, len(c.spcInfo))

	case kwatch.Deleted:
		if _, ok := c.spcInfo[spc.Name]; !ok {
			return true, fmt.Errorf("unsafe state. space (%s) was not " +
				"registered but we received event (%s)", spc.Name, event.Type)
		}
		c.unregisterSpace(spc.Name, true)
		c.log.Printf("space (%s) deleted. " +
			"There now %d spaces", spc.Name, len(c.spcInfo))
		return true, nil
	}
	return false,nil
}
