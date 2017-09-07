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
	//"k8s.io/client-go/rest"
	// Only required to authenticate against GKE clusters
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	log "github.com/sirupsen/logrus"
	"github.com/platform9/decco/pkg/controller"
)

func main() {
	log.Println("decco operator started!")

	/*
	config := k8sutil.GetClusterConfigOrDie()
	clientset := kubernetes.NewForConfigOrDie(config)
	ctrl := controller.New()
	*/

	for {
		c := controller.New("default")
		err := c.Run()
		switch err {
		case controller.ErrVersionOutdated:
			log.Infof("restarting controller due to ErrVersionOutdated")
		default:
			log.Fatalf("controller Run() ended with failure: %v", err)
		}
	}
	/*
	for {
		pods, err := clientset.CoreV1().Pods("").List(metav1.ListOptions{})
		if err != nil {
			panic(err.Error())
		}
		fmt.Printf("There are %d pods in the cluster\n", len(pods.Items))
		// Examples for error handling:
		// - Use helper functions like e.g. errors.IsNotFound()
		// - And/or cast to StatusError and use its properties like e.g. ErrStatus.Message
		_, err = clientset.CoreV1().Pods("default").Get("example-xxxxx", metav1.GetOptions{})
		if errors.IsNotFound(err) {
			fmt.Printf("Pod not found\n")
		} else if statusError, isStatus := err.(*errors.StatusError); isStatus {
			fmt.Printf("Error getting pod %v\n", statusError.ErrStatus.Message)
		} else if err != nil {
			panic(err.Error())
		} else {
			fmt.Printf("Found pod\n")
		}
		time.Sleep(10 * time.Second)
	}
	*/
}

