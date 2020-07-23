/*

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

package main

import (
	"flag"
	"os"

	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/platform9/decco/pkg/client"
	"github.com/platform9/decco/pkg/controllers"
	"github.com/platform9/decco/pkg/decco"
	"github.com/platform9/decco/pkg/dns"
	// +kubebuilder:scaffold:imports
)

var (
	setupLog = ctrl.Log.WithName("main")
)

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var disableAppReconciling bool
	var disableSpaceReconciling bool
	var dnsProviderName string
	var ingressIPOrHostname string

	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false, "Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&disableAppReconciling, "disable-app-controller", false, "Disable the App controller.")
	flag.BoolVar(&disableSpaceReconciling, "disable-space-controller", false, "Disable the Space controller.")
	flag.StringVar(&dnsProviderName, "dns-provider", "fake", "Set the DNS provider. Options: [fake,route53].")
	flag.StringVar(&ingressIPOrHostname, "ingress-addr", "", "Set a static ip or hostname for the cluster-wide ingress. If not set, Decco will look for decco/k8sniff.")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             client.GetScheme(),
		MetricsBindAddress: metricsAddr,
		LeaderElection:     enableLeaderElection,
		LeaderElectionID:   "decco.platform9.com",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	k8sRESTClient, err := client.New(config.GetConfigOrDie())
	if err != nil {
		setupLog.Error(err, "unable to create k8sRESTClient")
		os.Exit(1)
	}

	dnsProvider, err := dns.CreateProviderFromName(dnsProviderName)
	if err != nil {
		setupLog.Error(err, "unable to create dns provider", "controller", "Space")
		os.Exit(1)
	}

	var ingressClient decco.Ingress
	if len(ingressIPOrHostname) > 0 {
		setupLog.Info("Using static ingress to register DNS entries for.", "addr", ingressIPOrHostname)
		ingressClient = decco.NewStaticIngress(ingressIPOrHostname)
	} else {
		setupLog.Info("Using k8s service ingress to look up the address for registering DNS entries.", "svc", "decco/"+decco.DefaultK8sniffServiceName)
		ingressClient = decco.NewK8sServiceIngress("decco", decco.DefaultK8sniffServiceName, kubernetes.New(k8sRESTClient))
	}

	if !disableSpaceReconciling {
		spaceLogger := ctrl.Log.WithName("controllers").WithName("Space")

		if err = controllers.NewSpaceReconciler(mgr.GetClient(), dnsProvider, ingressClient, spaceLogger).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "Space")
			os.Exit(1)
		}
		setupLog.Info("Set up the SpaceReconciler.")
	} else {
		setupLog.Info("SpaceReconciler disabled.")
	}

	if !disableAppReconciling {
		if err = (&controllers.AppReconciler{
			Client: mgr.GetClient(),
			Log:    ctrl.Log.WithName("controllers").WithName("App"),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "App")
			os.Exit(1)
		}
		setupLog.Info("Set up the AppReconciler.")
	} else {
		setupLog.Info("AppReconciler disabled.")
	}

	// +kubebuilder:scaffold:builder

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
