package k8sutil

import (
	"os"

	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var DefaultKubeconfigPath string

func GetClusterConfigOrDie() *restclient.Config {
	kubeconfigPath := DefaultKubeconfigPath
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		kubeconfigPath = ""
	}
	// creates config
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		panic(err)
	}
	return config
}
