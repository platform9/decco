package k8sutil

import (
	netv1beta1 "k8s.io/api/networking/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
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
	localhostOnly bool,
	additionalAnnotations map[string]string,
) error {
	_, err := kubeApi.NetworkingV1beta1().Ingresses(ns).Create(NewHttpIngress(
		ns, name, labels, hostName, path, svcName, svcPort, rewritePath,
		encryptHttp, secretName, localhostOnly, additionalAnnotations,
	))
	return err
}

func NewHttpIngress(
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
	localhostOnly bool,
	additionalAnnotations map[string]string,
) *netv1beta1.Ingress {
	annotations := make(map[string]string)
	// Copy over additional annotations.
	// Note: this works even if additionalAnnotations is nil
	for key, val := range additionalAnnotations {
		annotations[key] = val
	}
	// temporary hack to work around nginx-to-stunnel timeouts during heavy load
	annotations["nginx.ingress.kubernetes.io/proxy-connect-timeout"] = "15"
	// some services like caproxy sometimes take longer than a minute to respond
	annotations["nginx.ingress.kubernetes.io/proxy-read-timeout"] = "600"
	annotations["nginx.ingress.kubernetes.io/ssl-redirect"] = "false"

	if rewritePath != "" {
		annotations["nginx.ingress.kubernetes.io/rewrite-target"] = rewritePath
	}
	if encryptHttp {
		annotations["nginx.ingress.kubernetes.io/secure-backends"] = "true"
	}
	ruleValue := netv1beta1.IngressRuleValue{
		HTTP: &netv1beta1.HTTPIngressRuleValue{
			Paths: []netv1beta1.HTTPIngressPath{
				{
					Path: path,
					Backend: netv1beta1.IngressBackend{
						ServiceName: svcName,
						ServicePort: intstr.IntOrString{
							Type:   intstr.Int,
							IntVal: svcPort,
						},
					},
				},
			},
		},
	}
	var tls []netv1beta1.IngressTLS
	rules := []netv1beta1.IngressRule{
		{
			Host:             "localhost",
			IngressRuleValue: ruleValue,
		},
	}
	if !localhostOnly {
		rules = append(rules, netv1beta1.IngressRule{
			Host:             hostName,
			IngressRuleValue: ruleValue,
		})
		tls = append(tls, netv1beta1.IngressTLS{
			Hosts: []string{
				hostName,
			},
			SecretName: secretName,
		})
	}
	return &netv1beta1.Ingress{
		TypeMeta: metav1.TypeMeta{
			APIVersion: netv1beta1.SchemeGroupVersion.String(),
			Kind:       "Ingress",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: netv1beta1.IngressSpec{
			Rules: rules,
			TLS:   tls,
		},
	}
}
