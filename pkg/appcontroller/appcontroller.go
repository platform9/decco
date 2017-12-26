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
	"io"
	"errors"
	"encoding/json"
	"github.com/sirupsen/logrus"
	"github.com/platform9/decco/pkg/k8sutil"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	kwatch "k8s.io/apimachinery/pkg/watch"
	"fmt"
	"k8s.io/client-go/kubernetes"
	"github.com/platform9/decco/pkg/app"
	"time"
	"net/http"
	"github.com/platform9/decco/pkg/spec"
	"github.com/platform9/decco/pkg/appspec"
	"github.com/platform9/decco/pkg/client"
	"sync"
	// "os"
	// "strconv"
)

var (
	initRetryWaitTime = 30 * time.Second
	// appCtrlShutdownDelaySeconds = 5
	ErrVersionOutdated = errors.New("(not a true error) watch needs to be " +
		"restarted to refresh resource version after a DELETED event")
	ErrTerminated = errors.New("gracefully terminated")
)

/*
func init() {
	delayStr := os.Getenv("APP_CONTROLLER_SHUTDOWN_DELAY_SECONDS")
	if delayStr != "" {
		var err error
		appCtrlShutdownDelaySeconds, err = strconv.Atoi(delayStr)
		if err != nil {
			logrus.Fatalf("failed to parse APP_CONTROLLER_SHUTDOWN_DELAY_SECONDS: %s", err)
		}
	}
	logrus.Println("appcontroller package initialized")
}
*/

type Event struct {
	Type   kwatch.EventType
	Object *appspec.App
}

type InternalController struct {
	log           *logrus.Entry
	apiHost       string
	extensionsApi apiextensionsclient.Interface
	kubeApi       kubernetes.Interface
	appInfo       map[string] AppInfo
	namespace     string
	spaceSpec     spec.SpaceSpec
	stopCh chan interface{}
}

type Controller struct {
	log           *logrus.Entry
	wg            *sync.WaitGroup
	stopCh        chan interface{}
	spaceSpec     spec.SpaceSpec
	namespace     string
}

type AppInfo struct {
	app *app.AppRuntime
	rscVersion *string
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
			case ErrTerminated:
				log.Infof("app controller for %s gracefully terminated",
					ctl.namespace)
				return
			case ErrVersionOutdated:
				log.Infof("restarting app controller for %s " +
					"due to ErrVersionOutdated", ctl.namespace)
			default:
				log.Warnf("restarting app controller for %s due to: %v",
					ctl.namespace, err)
				time.Sleep(2 * time.Second)
			}
		}
	}()
}

// ----------------------------------------------------------------------------

/*
func (ctl *Controller) Stop(allowDelayedAppCtrlShutdown bool) {
	go func() {
		if allowDelayedAppCtrlShutdown && appCtrlShutdownDelaySeconds > 0 {
			ctl.log.Printf("delaying app controller shutdown by %d seconds",
				appCtrlShutdownDelaySeconds)
			t := time.Second * time.Duration(appCtrlShutdownDelaySeconds)
			time.Sleep(t)
		}
		close(ctl.stopCh)
	}()
}
*/

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
		appInfo:       make(map[string] AppInfo),
		namespace:     namespace,
		spaceSpec:     spaceSpec,
		stopCh:        stopCh,
	}
}

// ----------------------------------------------------------------------------

func (ctl *InternalController) findAllApps() (string, error) {
	ctl.log.Info("finding existing apps ...")
	appList, err := k8sutil.GetAppList(
		ctl.kubeApi.CoreV1().RESTClient(), ctl.namespace)
	if err != nil {
		return "", err
	}

	for i := range appList.Items {
		a := appList.Items[i]

		if a.Status.IsFailed() {
			ctl.log.Infof("ignore failed app %s." +
				" Please delete its custom resource", a.Name)
			continue
		}

		a.Spec.Cleanup()
		initialRV := a.ResourceVersion
		newApp, err := app.New(a, ctl.kubeApi, ctl.namespace, ctl.spaceSpec)
		if err != nil {
			ctl.log.Warnf("app runtime creation failed: %s", err)
			continue
		}
		ctl.appInfo[a.Name] = AppInfo{
			app: newApp,
			rscVersion: &initialRV,
		}
	}

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

func (c *InternalController) initResource() (string, error) {
	watchVersion := "0"
	err := c.initCRD()
	if err != nil {
		if k8sutil.IsKubernetesResourceAlreadyExistError(err) {
			// CRD has been initialized before. We need to recover existing apps.
			watchVersion, err = c.findAllApps()
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

	c.log.Infof("app controller started in namespace %s " +
		"with %d app runtimes and initial watch version: %s",
		c.namespace, len(c.appInfo), watchVersion)
	err = c.watch(watchVersion, restClnt.Client, c.collectGarbage)
	return err
}

// ----------------------------------------------------------------------------

func (c *InternalController) collectGarbage() {
	knownUrlPaths := map[string] bool {}
	for _, info := range c.appInfo {
		urlPath := info.app.GetApp().Spec.HttpUrlPath
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

func (c *InternalController) watch(
	watchVersion string,
	httpClient *http.Client,
	periodicCallback func(),
) error {

	for {
		periodicCallback()
		resp, err := k8sutil.WatchApps(
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

func (c *InternalController) processWatchResponse(
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
		var chunk eventChunk
		select {
		case chunk = <- decodeOneChunk(decoder):
			break
		case <- c.stopCh:
			return "", ErrTerminated
		}
		ev, st, err := chunk.ev, chunk.st, chunk.err
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

		c.log.Debugf("app event: %v %v",
			ev.Type,
			ev.Object,
		)

		nextWatchVersion = ev.Object.ResourceVersion
		logrus.Infof("next watch version: %s", nextWatchVersion)
		if err := c.handleAppEvent(ev); err != nil {
			c.log.Warningf("event handler returned possible error: %v", err)
			return "", err
		}
	}
}

// ----------------------------------------------------------------------------

func (c *InternalController) handleAppEvent(event *Event) error {
	a := event.Object

	if a.Status.IsFailed() {
		// appsFailed.Inc()
		if event.Type == kwatch.Deleted {
			delete(c.appInfo, a.Name)
			return ErrVersionOutdated
		}
		c.log.Errorf("ignore failed a %s. Please delete its CR",
			a.Name)
		return nil
	}

	// TODO: add validation to appspec update.
	a.Spec.Cleanup()

	switch event.Type {
	case kwatch.Added:
		if _, ok := c.appInfo[a.Name]; ok {
			return fmt.Errorf("unsafe state. a (%s) was created" +
				" before but we received event (%s)", a.Name, event.Type)
		}

		newApp, err := app.New(*a, c.kubeApi, c.namespace, c.spaceSpec)
		if err != nil {
			c.log.Warnf("app runtime creation failed: %s", err)
			break
		}
		initialRV := a.ResourceVersion
		c.appInfo[a.Name] = AppInfo{
			app: newApp,
			rscVersion: &initialRV,
		}
		c.log.Printf("app (%s) added. There are now %d apps",
			a.Name, len(c.appInfo))

	case kwatch.Modified:
		if _, ok := c.appInfo[a.Name]; !ok {
			return fmt.Errorf("unsafe state. a (%s) was never" +
				" created but we received event (%s)", a.Name, event.Type)
		}
		c.appInfo[a.Name].app.Update(*a)
		*(c.appInfo[a.Name].rscVersion) = a.ResourceVersion
		c.log.Printf("app (%s) modified. There are now %d apps",
			a.Name, len(c.appInfo))

	case kwatch.Deleted:
		if _, ok := c.appInfo[a.Name]; !ok {
			return fmt.Errorf("unsafe state. a (%s) was never " +
				"created but we received event (%s)", a.Name, event.Type)
		}
		c.appInfo[a.Name].app.Delete()
		delete(c.appInfo, a.Name)
		c.log.Printf("app (%s) deleted. There are now %d apps",
			a.Name, len(c.appInfo))
		return ErrVersionOutdated
	}
	return nil
}
