package app

import (
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func Collect(kubeApi kubernetes.Interface,
	log *logrus.Entry,
	namespace string,
	isKnownApp func(name string) bool,
) {
	collectDeployments(kubeApi, log, namespace, isKnownApp)
	collectServices(kubeApi, log, namespace, isKnownApp)
	collectIngresses(kubeApi, log, namespace, isKnownApp)
}

func collectDeployments(kubeApi kubernetes.Interface,
	log *logrus.Entry,
	namespace string,
	isKnownApp func(name string) bool) {

	log = log.WithField("func", "collectDeployments")
	deplApi := kubeApi.ExtensionsV1beta1().Deployments(namespace)
	nses, err := deplApi.List(
		metav1.ListOptions{
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
			propPolicy := metav1.DeletePropagationBackground
			delOpts := metav1.DeleteOptions{PropagationPolicy: &propPolicy}
			err = deplApi.Delete(ns.Name, &delOpts)
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

	log = log.WithField("func", "collectServices")
	svcApi := kubeApi.CoreV1().Services(namespace)
	svcs, err := svcApi.List(
		metav1.ListOptions{
			LabelSelector: "decco-derived-from=app",
		},
	)
	if err != nil {
		log.Warnf("list services failed: %s", err.Error())
		return
	}
	log.Infof("there are %d services", len(svcs.Items))
	for _, svc := range svcs.Items {
		labels := svc.Labels
		appName, ok := labels["decco-app"]
		if !ok {
			log.Warnf("service %s has no decco-app label", svc.Name)
			continue
		}
		if !isKnownApp(appName) {
			log.Infof("deleting orphaned service %s", svc.Name)
			err = svcApi.Delete(svc.Name, nil)
			if err != nil {
				log.Warnf("failed to delete service %s: %s",
					svc.Name, err.Error())
			}
		}
	}
}

func collectIngresses(kubeApi kubernetes.Interface,
	log *logrus.Entry,
	namespace string,
	isKnownApp func(name string) bool) {

	log = log.WithField("func", "collectIngresses")
	ingApi := kubeApi.ExtensionsV1beta1().Ingresses(namespace)
	ingList, err := ingApi.List(
		metav1.ListOptions{
			LabelSelector: "decco-derived-from=app",
		},
	)
	if err != nil {
		log.Errorf("failed to list ingresses: %s", err)
		return
	}
	for _, ing := range ingList.Items {
		appName := ing.ObjectMeta.Labels["decco-app"]
		if appName == "" {
			log.Warnf("ingress '%s' has no decco-app label", ing.Name)
		}
		if appName == "" || !isKnownApp(appName) {
			log.Infof("deleting orphaned ingress '%s'", ing.Name)
			propPolicy := metav1.DeletePropagationBackground
			delOpts := metav1.DeleteOptions{PropagationPolicy: &propPolicy}
			err = ingApi.Delete(ing.Name, &delOpts)
			if err != nil {
				log.Warnf("failed to delete ingress %s: %s",
					ing.Name, err.Error())
			}
		}
	}
}
