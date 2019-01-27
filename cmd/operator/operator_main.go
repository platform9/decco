/*
Copyright 2017 Platform9 Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Note: the example only works with the code within the same release/branch.
package main

import (
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/kubernetes"
	log "github.com/sirupsen/logrus"
	"github.com/platform9/decco/pkg/controller"
	"github.com/platform9/decco/pkg/k8sutil"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"os"
	"sync"
	"time"
)

func main() {
	var namespace string
	namespace = os.Getenv("MY_POD_NAMESPACE")
	if len(namespace) == 0 {
		log.Fatalf("must set env MY_POD_NAMESPACE")
	}
	log.Println("decco operator started!")

	logLevelStr := os.Getenv("LOG_LEVEL")
	if logLevelStr == "" {
		logLevelStr = "info"
	}
	logLevel, err := log.ParseLevel(logLevelStr)
	if err != nil {
		log.Fatalf("failed to parse log level: %s", err)
	}
	log.SetLevel(logLevel)
	clustConfig := k8sutil.GetClusterConfigOrDie()
	kubeApi := kubernetes.NewForConfigOrDie(clustConfig)
	nsApi := kubeApi.CoreV1().Namespaces()
	nses, err := nsApi.List(
		meta_v1.ListOptions{
			LabelSelector: "contains-decco-spaces=true",
		},
	)
	if err != nil {
		log.Fatalf("list namespaces failed: %s", err.Error())
		return
	}
	if len(nses.Items) == 0 {
		log.Fatalf("there are no namesapces containing spaces")
		return
	}
	log.Infof("there are %d namespaces containing spaces",
		len(nses.Items))

	wg := sync.WaitGroup{}
	for _, ns := range nses.Items {
		wg.Add(1)
		log.Infof("spawning thread for %s", ns.Name)
		go func(namespace string) {
			for {
				c := controller.New(namespace, clustConfig, kubeApi)
				err := c.Run()
				switch err {
				default:
					log.Warnf("restarting controller for %s due to: %v",
						namespace, err)
					time.Sleep(2 * time.Second)
				}
			}
			log.Infof("thread for %s finished", namespace)
		} (ns.Name)
	}
	wg.Wait()
	log.Infof("all done")
}

