package appcontroller

import (
	"context"
	"github.com/platform9/decco/pkg/k8sutil"
	"github.com/platform9/decco/pkg/watcher"
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
	defer ctl.Stop()

	log := ctl.log.WithField("func", "shutdownWhenNamespaceGone")
	clustConfig := k8sutil.GetClusterConfigOrDie()
	kubeApi := kubernetes.NewForConfigOrDie(clustConfig)
	nsApi := kubeApi.CoreV1().Namespaces()
	restClient := kubeApi.CoreV1().RESTClient()
	sleepSeconds := 0
	ctx := context.Background()
	for {
		select {
		case <- ctl.stopCh:
			log.Debugf("graceful shut down detected in outer loop")
			return
		default:
		}
		if sleepSeconds > 0 {
			log.Infof("retrying in %d seconds", sleepSeconds)
			time.Sleep(time.Duration(sleepSeconds) * time.Second)
		}
		sleepSeconds += retryDelayIncrement
		fs := fmt.Sprintf("metadata.name=%s", ctl.namespace)
		nsList, err := nsApi.List(ctx, meta_v1.ListOptions{FieldSelector: fs})
		if err != nil {
			log.Warnf("failed to list namespaces %s", err)
			continue
		}
		rv := nsList.ResourceVersion
		log.Debugf("ns list rv: %s", rv)
		l := len(nsList.Items)
		if l != 1 {
			log.Errorf("ns list item count is unexpected: %d", l)
			continue
		}
		ns := nsList.Items[0]
		// rv = ns.ResourceVersion
		log.Debugf("ns rv: %s", ns.ResourceVersion)
		deleted, err := ctl.watchNamespaceInternal(fs, ns, restClient, rv)
		if err == watcher.ErrTerminated {
			log.Debugf("ns watch graceful shut down")
			return
		} else if err != nil {
			log.Errorf("watchNamespaceInternal failed: %s", err)
			continue
		}
		if deleted {
			// namespace has been deleted, initiate shutdown
			log.Infof("namespace deleted. Shutting down...")
			return
		}
		log.Infof("restarting watch due to stream closure")
		sleepSeconds = 0
		continue
		log.Errorf("did not find matching ns ... shutting down")
		return
	}
	log.Errorf("failed to read namespace too many times, shutting down")
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
		ctx := context.Background()
		w, err := restClient.
			Get().
			Resource("namespaces").
			VersionedParams(&listOpts, scheme.ParameterCodec).
			Watch(ctx)

		if err != nil {
			return false, fmt.Errorf("failed to watch namespace: %s", err)
		} else {
			events := w.ResultChan()
			for {
				select {
				case <- ctl.stopCh:
					return false, watcher.ErrTerminated
				case event := <- events:
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
}