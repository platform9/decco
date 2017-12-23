package appcontroller

import (
	"github.com/platform9/decco/pkg/k8sutil"
	"k8s.io/client-go/kubernetes"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"time"
	"fmt"
	"k8s.io/client-go/kubernetes/scheme"
	kwatch "k8s.io/apimachinery/pkg/watch"
)

var (
	retries = 5
	delayBetweenRetriesInSecs = 2
	watchTimeoutInSecs int64 = 20
)

func init() {

}

// Shut down the controller if the namespace is gone
func (ctl *Controller) watchNamespace() {
	log := ctl.log.WithField("func", "watchNamespace")
	clustConfig := k8sutil.GetClusterConfigOrDie()
	kubeApi := kubernetes.NewForConfigOrDie(clustConfig)
	nsApi := kubeApi.CoreV1().Namespaces()
	restClient := kubeApi.CoreV1().RESTClient()
	for i := 0; i < retries; i++ {
		if i > 0 {
			log.Infof("retrying in %d seconds", delayBetweenRetriesInSecs)
			time.Sleep(time.Duration(delayBetweenRetriesInSecs) * time.Second)
		}
		ns, err := nsApi.Get(ctl.namespace, meta_v1.GetOptions{})
		if err != nil {
			log.Warnf("failed to read namespace %s: %s",
				ctl.namespace, err)
			continue
		}
		fs := fmt.Sprintf("metadata.name=%s", ctl.namespace)
		listOpts := meta_v1.ListOptions{
			Watch: true,
			FieldSelector: fs,
			TimeoutSeconds: &watchTimeoutInSecs,
			ResourceVersion: ns.ResourceVersion,
		}
		log.Infof("watching namespace at rv %d", ns.ResourceVersion)
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
					if event.Type == kwatch.Deleted {
						log.Infof("namespace deleted, shutting down app controller")
						close(ctl.stopCh)
						return
					}
					if event.Type == "" {
						log.Infof("stream closed, restarting after delay")
						break
					}
				}
			}
			time.Sleep(time.Second * 2)
		}
	}
	log.Errorf("failed to read namespace too many times, shutting down")
	close(ctl.stopCh)
}