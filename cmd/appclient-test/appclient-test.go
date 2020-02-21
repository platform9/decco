/*
Copyright 2019 Platform9 Inc.

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
	"flag"

	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/klog"

	"github.com/platform9/decco/pkg/appspec"
	"github.com/platform9/decco/pkg/client"
	"github.com/platform9/decco/pkg/k8sutil"
)

func main() {
	klog.InitFlags(nil)
	flag.Parse()
	restConfig := k8sutil.GetClusterConfigOrDie()
	restCli, err := client.NewAppClient(restConfig)
	if err != nil {
		log.Fatalf("failed to get restCli: %s", err)
	}
	app := appspec.App{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-1",
		},
	}
	var rtObj runtime.Object
	rtObj = &app
	err = restCli.Post().Namespace("test-kplane-leb-2416").
		Resource(appspec.CRDResourcePlural).
		Body(rtObj).Do().Into(nil)
	if err != nil {
		log.Fatalf("failed to create app: %s", err)
	}
}
