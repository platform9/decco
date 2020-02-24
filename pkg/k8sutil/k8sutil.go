package k8sutil

import (
	"flag"
	"os"
	"path/filepath"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var kubeconfig *string

func init() {
	if home := homeDir(); home != "" {
		kubeconfig = flag.String(
			"kubeconfig",
			filepath.Join(home, ".kube", "config"),
			"(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()
}

func GetClusterConfigOrDie() *restclient.Config {
	if _, err := os.Stat(*kubeconfig); os.IsNotExist(err) {
		empty := ""
		kubeconfig = &empty // assume in-cluster configuration if kubeconfig does not exist
	}
	// creates the in-cluster config
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	// config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}
	return config
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}

func IsKubernetesResourceAlreadyExistError(err error) bool {
	return apierrors.IsAlreadyExists(err)
}
