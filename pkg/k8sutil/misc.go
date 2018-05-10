package k8sutil

import (
	"fmt"
	"k8s.io/client-go/kubernetes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GetTcpIngressIpOrHostname(kubeApi kubernetes.Interface) (string, bool, error) {
	svcApi := kubeApi.CoreV1().Services("decco")
	svc, err := svcApi.Get("k8sniff", metav1.GetOptions{})
	if err != nil {
		return "", false, fmt.Errorf("failed to get k8sniff service: %s", err)
	}
	lbIngresses := svc.Status.LoadBalancer.Ingress;
	if len(lbIngresses) == 0 {
		return "", false, fmt.Errorf("k8sniff service has no LB ingresses")
	}
	ip := lbIngresses[0].IP
	if len(ip) > 0 {
		return ip, false, nil
	}
	hostname := lbIngresses[0].Hostname
	if len(hostname) > 0 {
		return hostname, true, nil
	}
	return "", false, fmt.Errorf("k8sniff service has no IP or hostname")
}

