package k8sutil

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)


func CreateHttpIngress(
	kubeApi kubernetes.Interface,
	ns string,
	name string,
	labels map[string]string,
	hostName string,
	path string,
	svcName string,
	svcPort int32,
	rewritePath string,
	encryptHttp bool,
	secretName string,
) error {
	ingApi := kubeApi.ExtensionsV1beta1().Ingresses(ns)
	annotations := make(map[string]string)

	// temporary hack to work around nginx-to-stunnel timeouts during heavy load
	annotations["ingress.kubernetes.io/proxy-connect-timeout"] = "15"

	if rewritePath != "" {
		annotations["ingress.kubernetes.io/rewrite-target"] = rewritePath
	}
	if encryptHttp {
		annotations["ingress.kubernetes.io/secure-backends"] = "true"
	}
	ing := v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: labels,
			Annotations: annotations,
		},
		Spec: v1beta1.IngressSpec{
			Rules: []v1beta1.IngressRule{
				{
					Host: hostName,
					IngressRuleValue: v1beta1.IngressRuleValue {
						HTTP: &v1beta1.HTTPIngressRuleValue{
							Paths: []v1beta1.HTTPIngressPath {
								{
									Path: path,
									Backend: v1beta1.IngressBackend{
										ServiceName: svcName,
										ServicePort: intstr.IntOrString {
											Type: intstr.Int,
											IntVal: svcPort,
										},
									},
								},
							},
						},
					},
				},
			},
			TLS: []v1beta1.IngressTLS {
				{
					Hosts: []string {
						hostName,
					},
					SecretName: secretName,
				},
			},
		},
	}
	_, err := ingApi.Create(&ing)
	return err
}
