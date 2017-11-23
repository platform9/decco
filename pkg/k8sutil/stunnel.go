package k8sutil

import (
	"fmt"
	"k8s.io/api/core/v1"
)

const TlsPort = 443

// -----------------------------------------------------------------------------

func stunnelEnvVars(verifyChain string, port int32, tlsSecretName string,
	isNginxIngressStyleCertSecret bool) []v1.EnvVar {
	stunnelEnv := []v1.EnvVar {
		{
			Name: "STUNNEL_VERIFY_CHAIN",
			Value: verifyChain,
		},
		{
			Name: "STUNNEL_CONNECT",
			Value: fmt.Sprintf("%d", port),
		},
	}
	if isNginxIngressStyleCertSecret {
		// The server cert file names are different because they follow
		// the nginx ingress controller conventions (tls.crt and tls.key).
		// There is no CA for client certificate verification (for now).
		stunnelEnv = append(stunnelEnv,
			v1.EnvVar{
				Name: "STUNNEL_CERT_FILE",
				Value: "/etc/stunnel/certs/tls.crt",
			},
			v1.EnvVar{
				Name: "STUNNEL_KEY_FILE",
				Value: "/etc/stunnel/certs/tls.key",
			},
		)
	}
	return stunnelEnv
}

// -----------------------------------------------------------------------------

func InsertStunnel(
	verifyChain string,
	port int32,
	tlsSecretName string,
	isNginxIngressStyleCertSecret bool,
	volumes []v1.Volume,
	containers []v1.Container,
) ([]v1.Volume, []v1.Container) {

	stunnelEnv := stunnelEnvVars(verifyChain, port, tlsSecretName,
		isNginxIngressStyleCertSecret)

	volumes = append(volumes, v1.Volume{
		Name: "certs",
		VolumeSource: v1.VolumeSource{
			Secret: &v1.SecretVolumeSource{
				SecretName: tlsSecretName,
			},
		},
	})
	containers = append(containers, v1.Container{
		Name: "stunnel",
		Image: "platform9systems/stunnel",
		Ports: []v1.ContainerPort{
			{
				ContainerPort: TlsPort,
			},
		},
		Env: stunnelEnv,
		VolumeMounts: []v1.VolumeMount{
			{
				Name: "certs",
				ReadOnly: true,
				MountPath: "/etc/stunnel/certs",
			},
		},
	})
	return volumes, containers
}


