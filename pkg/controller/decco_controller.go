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
	"github.com/platform9/decco/pkg/custregion"
	"time"
	"github.com/coreos/etcd-operator/pkg/util/probe"
	"sync"
	"net/http"
	"github.com/platform9/decco/pkg/spec"
	"github.com/platform9/decco/pkg/client"
)

var (
	initRetryWaitTime = 30 * time.Second
	ErrVersionOutdated = errors.New("(not a true error) watch needs to be " +
		"restarted to refresh resource version after a DELETED event")
)


func init() {
	logrus.Println("controller package initialized")
}

type Event struct {
	Type   kwatch.EventType
	Object *spec.CustomerRegion
}

type Controller struct {
	log *logrus.Entry
	apiHost string
	extensionsApi apiextensionsclient.Interface
	kubeApi kubernetes.Interface
	namespace string
	crInfo map[string] CustRegionInfo
	waitCustomerRegion sync.WaitGroup
}

type CustRegionInfo struct {
	custRegion *custregion.CustomerRegionRuntime
	rscVersion *string
	stopCh chan struct{}
}

// ----------------------------------------------------------------------------

func New(ns string) *Controller {
	clustConfig := k8sutil.GetClusterConfigOrDie()
	logger := logrus.WithField("pkg", "controller")
	logger.Logger.SetLevel(logrus.DebugLevel)
	return &Controller{
		log: logger,
		apiHost: clustConfig.Host,
		extensionsApi: k8sutil.MustNewKubeExtClient(),
		kubeApi: kubernetes.NewForConfigOrDie(clustConfig),
		namespace: ns,
		crInfo: make(map[string] CustRegionInfo),
	}
}

// ----------------------------------------------------------------------------

func (ctl *Controller) findAllCustomerRegions() (string, error) {
	ctl.log.Info("finding existing customerRegions...")
	crgList, err := k8sutil.GetCustomerRegionList(
		ctl.kubeApi.CoreV1().RESTClient(),
		ctl.namespace)
	if err != nil {
		return "", err
	}

	for i := range crgList.Items {
		crg := crgList.Items[i]

		if crg.Status.IsFailed() {
			ctl.log.Infof("ignore failed customerRegion (%s). Please delete its custom resource", crg.Name)
			continue
		}

		crg.Spec.Cleanup()

		stopC := make(chan struct{})
		initialRV := crg.ResourceVersion
		newCr := custregion.New(
			crg,
			ctl.kubeApi,
			stopC,
			&ctl.waitCustomerRegion,
		)
		ctl.crInfo[crg.Name] = CustRegionInfo{
			stopCh: stopC,
			custRegion: newCr,
			rscVersion: &initialRV,
		}
	}

	return crgList.ResourceVersion, nil
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
			// CRD has been initialized before. We need to recover existing customer regions.
			watchVersion, err = c.findAllCustomerRegions()
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

	c.log.Infof("controller initial watch version: %s", watchVersion)

	defer func() {
		c.log.Infof("waiting for cust region workers to exit")
		for _, crInfo := range c.crInfo {
			close(crInfo.stopCh)
		}
		c.waitCustomerRegion.Wait()
		c.log.Infof("all cust region workers have exited")
	}()

	probe.SetReady()
	err = c.watch(watchVersion, restClnt.Client)
	/*
	go func() {
		//pt := newPanicTimer(time.Minute, "unexpected long blocking (> 1 Minute) when handling cluster event")

		for ev := range eventCh {
			//pt.start()
			if err := c.handleCustRegEvent(ev); err != nil {
				c.log.Warningf("fail to handle event: %v", err)
			}
			//pt.stop()
		}
	}()
	*/
	return err
}

// ----------------------------------------------------------------------------

// watch creates a go routine, and watches the customer region resources from
// the given watch version. It emits events on the resources through the returned
// event chan. Errors will be reported through the returned error chan. The go routine
// exits on any error.
func (c *Controller) watch(watchVersion string, httpClient *http.Client) error {

	for {
		resp, err := k8sutil.WatchCustomerRegions(
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
				c.log.Info("apiserver closed watch stream, retrying after 5s...")
				time.Sleep(5 * time.Second)
				return nextWatchVersion, nil
			}
			c.log.Errorf("received invalid event from watch API: %v", err)
			return "", err
		}

		if st != nil {
			err = fmt.Errorf("unexpected watch error: %v", st)
			return "", err
		}

		c.log.Debugf("customer region event: %v %v",
			ev.Type,
			ev.Object,
		)

		nextWatchVersion = ev.Object.ResourceVersion
		logrus.Infof("next watch version: %s", nextWatchVersion)
		if err := c.handleCustRegEvent(ev); err != nil {
			c.log.Warningf("event handler returned possible error: %v", err)
			return "", err
		}
	}
}

// ----------------------------------------------------------------------------

func (c *Controller) handleCustRegEvent(event *Event) error {
	crg := event.Object

	if crg.Status.IsFailed() {
		// custRegsFailed.Inc()
		if event.Type == kwatch.Deleted {
			delete(c.crInfo, crg.Name)
			return nil
		}
		return fmt.Errorf("ignore failed custReg (%s). Please delete its CR", crg.Name)
	}

	// TODO: add validation to spec update.
	crg.Spec.Cleanup()

	switch event.Type {
	case kwatch.Added:
		if _, ok := c.crInfo[crg.Name]; ok {
			return fmt.Errorf("unsafe state. custReg (%s) was created before but we received event (%s)", crg.Name, event.Type)
		}

		stopC := make(chan struct{})
		newCustReg := custregion.New(*crg, c.kubeApi, stopC, &c.waitCustomerRegion)
		initialRV := crg.ResourceVersion
		c.crInfo[crg.Name] = CustRegionInfo{
			stopCh: stopC,
			custRegion: newCustReg,
			rscVersion: &initialRV,
		}
		c.log.Printf("customer region (%s) added. There are now (%d)",
			crg.Name, len(c.crInfo))
		/*
		analytics.CustRegCreated()
		custRegsCreated.Inc()
		custRegustotal.Inc()
		*/

	case kwatch.Modified:
		if _, ok := c.crInfo[crg.Name]; !ok {
			return fmt.Errorf("unsafe state. custReg (%s) was never created but we received event (%s)", crg.Name, event.Type)
		}
		c.crInfo[crg.Name].custRegion.Update(*crg)
		*(c.crInfo[crg.Name].rscVersion) = crg.ResourceVersion
		c.log.Printf("customer region (%s) modified. There are now (%d)",
			crg.Name, len(c.crInfo))
		//custRegsModified.Inc()

	case kwatch.Deleted:
		if _, ok := c.crInfo[crg.Name]; !ok {
			return fmt.Errorf("unsafe state. custReg (%s) was never created but we received event (%s)", crg.Name, event.Type)
		}
		delete(c.crInfo, crg.Name)
		c.log.Printf("customer region (%s) deleted. There are now (%d)",
			crg.Name, len(c.crInfo))
		return ErrVersionOutdated
		/*
		analytics.CustRegDeleted()
		custRegsDeleted.Inc()
		custRegustotal.Dec()
		*/
	}
	return nil
}
