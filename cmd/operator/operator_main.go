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
	log "github.com/sirupsen/logrus"
	"github.com/platform9/decco/pkg/controller"
)

func main() {
	log.Println("decco operator started!")

	for {
		c := controller.New()
		err := c.Run()
		switch err {
		case controller.ErrVersionOutdated:
			log.Infof("restarting controller due to ErrVersionOutdated")
		default:
			log.Fatalf("controller Run() ended with failure: %v", err)
		}
	}
}

