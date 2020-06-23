package k8sutil

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetTcpIngressIpOrHostname(kubeApi kubernetes.Interface) (ipOrHostname string, isHostname bool, err error) {
	svcApi := kubeApi.CoreV1().Services("decco")
	svc, err := svcApi.Get("k8sniff", metav1.GetOptions{})
	if err != nil {
		return "", false, fmt.Errorf("failed to get k8sniff service: %s", err)
	}
	return getTcpIngressIpOrHostnameFromSvc(svc)
}

func GetTcpIngressIPOrHostnameWithControllerClient(ctx context.Context, client client.Client) (ipOrHostname string, isHostname bool, err error) {
	svc := &v1.Service{}
	err = client.Get(ctx, types.NamespacedName{
		Namespace: "decco",
		Name:      "k8sniff",
	}, svc)
	if err != nil {
		return "", false, fmt.Errorf("failed to get k8sniff service: %s", err)
	}
	return getTcpIngressIpOrHostnameFromSvc(svc)
}

func getTcpIngressIpOrHostnameFromSvc(svc *v1.Service) (ipOrHostname string, isHostname bool, err error) {
	lbIngresses := svc.Status.LoadBalancer.Ingress
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
