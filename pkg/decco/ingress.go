package decco

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

const (
	DefaultK8sniffServiceName = "k8sniff"
)

// Ingress is responsible for all functionality related to the Decco-wide
// ingress controller.
type Ingress interface {
	GetIPOrHostname(ctx context.Context) (string, error)
}

type K8sServiceIngress struct {
	namespace string
	name      string
	k8sClient kubernetes.Interface
}

func NewK8sServiceIngress(namespace string, name string, k8sClient kubernetes.Interface) *K8sServiceIngress {
	return &K8sServiceIngress{namespace: namespace, name: name, k8sClient: k8sClient}
}

func NewDefaultK8sServiceIngress(k8sClient kubernetes.Interface) *K8sServiceIngress {
	return NewK8sServiceIngress("", DefaultK8sniffServiceName, k8sClient)
}

func (k *K8sServiceIngress) GetIPOrHostname(_ context.Context) (string, error) {
	// TODO(erwin) replace with injected k.k8sclient, but that currently errors: {"controller": "space", "name": "example-space-1", "namespace": "default", "error": "failed to reconcile the DNS records: failed to get TCP ingress ipOrHostname: failed to get service (decco/k8sniff): the server could not find the requested resource (get services.decco.platform9.com k8sniff)"}
	cfg := config.GetConfigOrDie()
	client := kubernetes.NewForConfigOrDie(cfg)
	svc, err := client.CoreV1().Services(k.namespace).Get(k.name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get service (%s/%s): %w", k.namespace, k.name, err)
	}

	lbIngresses := svc.Status.LoadBalancer.Ingress
	if len(lbIngresses) == 0 {
		return "", fmt.Errorf("service %s/%s has no LB ingresses", k.namespace, k.name)
	}
	ip := lbIngresses[0].IP
	if len(ip) > 0 {
		return ip, nil
	}
	hostname := lbIngresses[0].Hostname
	if len(hostname) > 0 {
		return hostname, nil
	}

	return "", fmt.Errorf("service has no IP or hostname")
}

type StaticIngress struct {
	ipOrHostname string
}

func NewStaticIngress(ipOrHostname string) *StaticIngress {
	return &StaticIngress{ipOrHostname: ipOrHostname}

}

func (s *StaticIngress) GetIPOrHostname(_ context.Context) (string, error) {
	return s.ipOrHostname, nil
}
