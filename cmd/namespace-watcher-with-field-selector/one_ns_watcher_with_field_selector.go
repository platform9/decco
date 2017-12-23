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
	//	"k8s.io/client-go/kubernetes/typed/core/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/kubernetes"
	log "github.com/sirupsen/logrus"
	"github.com/platform9/decco/pkg/k8sutil"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	scheme "k8s.io/client-go/kubernetes/scheme"
	"time"
)

func main() {
	restConfig := k8sutil.GetClusterConfigOrDie()
	kubeApi := kubernetes.NewForConfigOrDie(restConfig)
	restClient := kubeApi.CoreV1().RESTClient()
	listOpts := meta_v1.ListOptions{
		Watch: true,
		FieldSelector: "metadata.name=test-ns-1",
	}
	for {
		watcher, err := restClient.
			Get().
			Resource("namespaces").
			VersionedParams(&listOpts, scheme.ParameterCodec).
			Watch()

		if err != nil {
			log.Warnf("failed to watch namespace: %s", err)
		} else {
			events := watcher.ResultChan()
			for {
				event := <- events
				log.Infof("%s %v", event.Type, event.Object)
				if event.Type == "" {
					log.Infof("stream closed, restarting after delay")
					break
				}
			}
		}
		time.Sleep(time.Second * 2)
	}
}



