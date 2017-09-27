package app

import (
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"github.com/sirupsen/logrus"
)

func Collect(kubeApi kubernetes.Interface,
	log *logrus.Entry,
	namespace string,
	isKnownApp func(name string) bool) {

	collectDeployments(kubeApi, log, namespace, isKnownApp)
	collectServices(kubeApi, log, namespace, isKnownApp)
}

func collectDeployments(kubeApi kubernetes.Interface,
	log *logrus.Entry,
	namespace string,
	isKnownApp func(name string) bool) {

	log = log.WithField("func", "collect")
	deplApi := kubeApi.ExtensionsV1beta1().Deployments(namespace)
	nses, err := deplApi.List(
		meta_v1.ListOptions{
			LabelSelector: "decco-derived-from=app",
		},
	)
	if err != nil {
		log.Warnf("list deployments failed: %s", err.Error())
		return
	}
	log.Infof("there are %d deployments", len(nses.Items))
	for _, ns := range nses.Items {
		if !isKnownApp(ns.Name) {
			log.Infof("deleting orphaned deployment %s", ns.Name)
			err = deplApi.Delete(ns.Name, nil)
			if err != nil {
				log.Warnf("failed to delete deployment %s: %s",
					ns.Name, err.Error())
			}
		}
	}
}

func collectServices(kubeApi kubernetes.Interface,
	log *logrus.Entry,
	namespace string,
	isKnownApp func(name string) bool) {

	log = log.WithField("func", "collect")
	svcApi := kubeApi.CoreV1().Services(namespace)
	nses, err := svcApi.List(
		meta_v1.ListOptions{
			LabelSelector: "decco-derived-from=app",
		},
	)
	if err != nil {
		log.Warnf("list services failed: %s", err.Error())
		return
	}
	log.Infof("there are %d services", len(nses.Items))
	for _, ns := range nses.Items {
		if !isKnownApp(ns.Name) {
			log.Infof("deleting orphaned service %s", ns.Name)
			err = svcApi.Delete(ns.Name, nil)
			if err != nil {
				log.Warnf("failed to delete service %s: %s",
					ns.Name, err.Error())
			}
		}
	}
}
