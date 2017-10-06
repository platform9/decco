package app

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"github.com/sirupsen/logrus"
	"k8s.io/api/extensions/v1beta1"
)

func Collect(kubeApi kubernetes.Interface,
	log *logrus.Entry,
	namespace string,
	isKnownApp func(name string) bool,
	isKnownUrlPath func(name string) bool,
) {
	collectDeployments(kubeApi, log, namespace, isKnownApp)
	collectServices(kubeApi, log, namespace, isKnownApp)
	collectUrlPaths(kubeApi, log, namespace, isKnownUrlPath)
	collectBinaryIngresses(kubeApi, log, namespace, isKnownApp)
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
		if !isKnownApp(svc.Name) {
			log.Infof("deleting orphaned service %s", svc.Name)
			err = svcApi.Delete(svc.Name, nil)
			if err != nil {
				log.Warnf("failed to delete service %s: %s",
					svc.Name, err.Error())
			}
		}
	}
}

func collectUrlPaths(kubeApi kubernetes.Interface,
	log *logrus.Entry,
	namespace string,
	isKnownUrlPath func(name string) bool) {

	log = log.WithField("func", "collectUrlPaths")
	ingApi := kubeApi.ExtensionsV1beta1().Ingresses(namespace)
	ing, err := ingApi.Get("http-ingress", metav1.GetOptions{})
	if err != nil {
		log.Errorf("failed to get http ingress: %s", err)
		return
	}
	rules := ing.Spec.Rules
	if len(rules) != 1 {
		log.Errorf("http-ingress has invalid number of rules: %d",
			len(rules))
		return	
	}
	paths := rules[0].IngressRuleValue.HTTP.Paths
	if len(paths) < 1 {
		log.Errorf("http-ingress has no paths")
		return
	}
	dirty := false
	newPaths := []v1beta1.HTTPIngressPath {}
	for _, path := range paths {
		if path.Path != "/"  {
			if isKnownUrlPath(path.Path) {
				newPaths = append(newPaths, path)
			} else {
				log.Warnf("deleting orphaned url path: %s", path.Path)
				dirty = true
			}
		} else {
			newPaths = append(newPaths, path)
		}
	}
	if dirty {
		rules[0].IngressRuleValue.HTTP.Paths = newPaths
		_, err = ingApi.Update(ing)
		if err != nil {
			log.Errorf("failed to update http ingress: %s", err)
		}
	}
}

func collectBinaryIngresses(kubeApi kubernetes.Interface,
	log *logrus.Entry,
	namespace string,
	isKnownApp func(name string) bool) {

	log = log.WithField("func", "collectBinaryIngresses")
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
		if !isKnownApp(ing.Name) {
			log.Infof("deleting orphaned ingress %s", ing.Name)
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