package k8sutil

import (
	"context"
	"fmt"
	"os"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func GetTcpIngressIpOrHostname(kubeApi kubernetes.Interface) (string, bool, error) {
	namespace := "decco"
	service := "k8sniff"
	if ns := os.Getenv("LB_NAMESPACE"); ns != "" {
		namespace = ns
	}
	if svc := os.Getenv("LB_SERVICE"); svc != "" {
		service = svc
	}
	svcApi := kubeApi.CoreV1().Services(namespace)
	ctx := context.Background()
	svc, err := svcApi.Get(ctx, service, metav1.GetOptions{})
	if err != nil {
		return "", false, fmt.Errorf("failed to get Service %s in Namespace %s: %s", service, namespace, err)
	}
	lbIngresses := svc.Status.LoadBalancer.Ingress
	if len(lbIngresses) == 0 {
		return "", false, fmt.Errorf("Service %s in Namespace %s has no LB ingresses", service, namespace)
	}
	ip := lbIngresses[0].IP
	if len(ip) > 0 {
		return ip, false, nil
	}
	hostname := lbIngresses[0].Hostname
	if len(hostname) > 0 {
		return hostname, true, nil
	}
	return "", false, fmt.Errorf("Service %s in Namespace %s has no IP or hostname", service, namespace)
}
