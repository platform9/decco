package custregion

import (
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
)

func Collect(kubeApi kubernetes.Interface,
	log *logrus.Entry,
	isKnownCustRegion func(name string) bool) {

	log = log.WithField("func", "collect")
	nsApi := kubeApi.CoreV1().Namespaces()
	nses, err := nsApi.List(
		meta_v1.ListOptions{
			LabelSelector: "app=decco",
		},
	)
	if err != nil {
		log.Warnf("list namespaces failed: %s", err.Error())
		return
	}
	log.Infof("there are %d namespaces", len(nses.Items))
	for _, ns := range nses.Items {
		if !isKnownCustRegion(ns.Name) {
			if ns.Status.Phase == v1.NamespaceTerminating {
				log.Debugf("skipping deletion of terminating namespace %s",
					ns.Name)
				continue
			}
			log.Infof("deleting orphaned namespace %s", ns.Name)
			err = nsApi.Delete(ns.Name, nil)
			if err != nil {
				log.Warnf("failed to delete namespace %s: %s",
					ns.Name, err.Error())
			}
		}
	}
}
