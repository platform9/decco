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
	"os"
	"time"

	"github.com/platform9/decco/pkg/k8sutil"
	"github.com/platform9/decco/pkg/spacecontroller"
	log "github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

func main() {
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
	for {
		c := spacecontroller.New(clustConfig, kubeApi)
		err := c.Run()
		switch err {
		default:
			log.Warnf("restarting controller due to: %v", err)
			time.Sleep(2 * time.Second)
		}
	}
}
