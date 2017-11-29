package k8sutil

import (
	"fmt"
	"k8s.io/client-go/kubernetes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GetTcpIngressIp(kubeApi kubernetes.Interface) (string, error) {
	svcApi := kubeApi.CoreV1().Services("decco")
	svc, err := svcApi.Get("k8sniff", metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get k8sniff service: %s", err)
	}
	lbIngresses := svc.Status.LoadBalancer.Ingress;
	if len(lbIngresses) == 0 {
		return "", fmt.Errorf("k8sniff service has no LB ingresses")
	}
	ip := lbIngresses[0].IP
	if len(ip) == 0 {
		return "", fmt.Errorf("k8sniff service has no IP")
	}
	return ip, nil
}

