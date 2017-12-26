package appcontroller

import (
	"github.com/platform9/decco/pkg/k8sutil"
	"k8s.io/client-go/kubernetes"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/api/core/v1"
	"time"
	"fmt"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	kwatch "k8s.io/apimachinery/pkg/watch"
	"strconv"
	"github.com/sirupsen/logrus"
	"os"
)

var (
	retryDelayIncrement = 2
	watchTimeoutInSecs int64 = 20
)

func init() {
	delayStr := os.Getenv("WATCH_TIMEOUT_SECONDS")
	if delayStr != "" {
		delay, err := strconv.Atoi(delayStr)
		if err != nil {
			logrus.Fatalf("failed to parse WATCH_TIMEOUT_SECONDS: %s", err)
		}
		watchTimeoutInSecs = int64(delay)
	}
}

// Shut down the controller if the namespace is gone
func (ctl *Controller) shutdownWhenNamespaceGone() {
	defer close(ctl.stopCh)

	log := ctl.log.WithField("func", "shutdownWhenNamespaceGone")
	clustConfig := k8sutil.GetClusterConfigOrDie()
	kubeApi := kubernetes.NewForConfigOrDie(clustConfig)
	nsApi := kubeApi.CoreV1().Namespaces()
	restClient := kubeApi.CoreV1().RESTClient()
	sleepSeconds := 0

	listLoop:
	for {
		if sleepSeconds > 0 {
			log.Infof("retrying in %d seconds", sleepSeconds)
			time.Sleep(time.Duration(sleepSeconds) * time.Second)
		}
		sleepSeconds += retryDelayIncrement
		fs := fmt.Sprintf("metadata.name=%s", ctl.namespace)
		nsList, err := nsApi.List(meta_v1.ListOptions{FieldSelector: fs})
		if err != nil {
			log.Warnf("failed to list namespaces %s", err)
			continue
		}
		rv := nsList.ResourceVersion
		log.Debugf("ns list rv: %s", rv)
		for _, ns := range nsList.Items {
			if ns.Name == ctl.namespace {
				deleted, err := ctl.watchNamespaceInternal(fs, ns,
					restClient, rv)
				if err != nil {
					log.Warnf("watchNamespaceInternal failed: %s")
					continue listLoop
				}
				if deleted {
					// namespace has been deleted, initiate shutdown
					log.Infof("namespace deleted. Shutting down...")
					return
				}
				log.Infof("restarting watch due to stream closure")
				sleepSeconds = 0
				continue listLoop
			}
		}
		log.Errorf("did not find matching ns ... shutting down")
		return
	}
	log.Errorf("failed to read namespace too many times, shutting down")
	close(ctl.stopCh)
}

// watch namespace until it is deleted
// returns true if namespace has been deleted, and false if watch stream
// closed and needs to be restarted with a new resource version
func (ctl *Controller) watchNamespaceInternal(
	fs string,
	ns v1.Namespace,
	restClient rest.Interface,
	resourceVersion string,
) (bool, error) {

	log := ctl.log.WithField("func", "watchNamespaceInternal")
	listOpts := meta_v1.ListOptions{
		Watch: true,
		FieldSelector: fs,
		TimeoutSeconds: &watchTimeoutInSecs,
		ResourceVersion: resourceVersion,
	}
	log.Infof("watching namespace at rv %s", resourceVersion)
	for {
		watcher, err := restClient.
			Get().
			Resource("namespaces").
			VersionedParams(&listOpts, scheme.ParameterCodec).
			Watch()

		if err != nil {
			return false, fmt.Errorf("failed to watch namespace: %s", err)
		} else {
			events := watcher.ResultChan()
			for {
				event := <- events
				if event.Type == "" {
					log.Debugf("stream closed")
					return false, nil
				}
				log.Infof("%s %v", event.Type, event.Object)
				if event.Type == kwatch.Deleted {
					log.Debugf("namespace deleted")
					return true, nil
				}
			}
		}
	}
}