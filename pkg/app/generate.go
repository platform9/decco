package app

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/api/networking/v1beta1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"

	deccov1beta2 "github.com/platform9/decco/api/v1beta2"
	"github.com/platform9/decco/pkg/k8sutil"
)

// GenerateResources generates the Kubernetes resources (services, deploymentes, etc.) for the provided App within the provided space.
//
// From the space the domainName (required), HttpCertSecretName (required),
// EncryptHttp (optional), and TcpCertAndCaSecretName (optional) are used.
func GenerateResources(spaceSpec *deccov1beta2.SpaceSpec, app *deccov1beta2.App) ([]runtime.Object, error) {
	if err := spaceSpec.Validate(); err != nil {
		return nil, err
	}
	if err := app.Spec.Validate(spaceSpec.TcpCertAndCaSecretName); err != nil {
		return nil, err
	}

	var objects []runtime.Object
	stunnelIndex := 0
	podSpec := app.Spec.PodSpec.DeepCopy()
	insertDomainEnvVarIntoPod(spaceSpec, app, podSpec)

	// Permissions
	objs, err := generateRBAC(app, podSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to set up permissions: %s", err)
	}
	objects = append(objects, objs...)

	// Services and ingresses
	objs, err = generateEndpoints(spaceSpec, app, podSpec, &stunnelIndex)
	if err != nil {
		return nil, fmt.Errorf("failed to create endpoints: %s", err)
	}
	objects = append(objects, objs...)

	// Deployment
	obj, err := generateDeploymentOrJob(spaceSpec, app, podSpec, stunnelIndex)
	if err != nil {
		return nil, fmt.Errorf("failed to create deployment: %s", err)
	}

	return append(objects, obj), nil
}

// insertDomainEnvVarIntoPod mutates the provided podSpec, adding the
// DomainEnvVarName to the environment of each container.
func insertDomainEnvVarIntoPod(spaceSpec *deccov1beta2.SpaceSpec, app *deccov1beta2.App, podSpec *v1.PodSpec) {
	if app.Spec.DomainEnvVarName == "" {
		return
	}
	for i := range podSpec.Containers {
		podSpec.Containers[i].Env = append(podSpec.Containers[i].Env, v1.EnvVar{
			Name:  app.Spec.DomainEnvVarName,
			Value: spaceSpec.DomainName,
		})
	}
}

// generateRBAC generates RBAC resources for the app.
//
// It mutates the provided podSpec by optionally setting the ServiceAccountName.
func generateRBAC(app *deccov1beta2.App, podSpec *v1.PodSpec) ([]runtime.Object, error) {
	var objects []runtime.Object

	rules := app.Spec.Permissions
	if rules == nil || len(rules) == 0 {
		return nil, nil
	}

	saName := podSpec.ServiceAccountName
	if saName == "" {
		saName = app.Name
		objects = append(objects, &v1.ServiceAccount{
			TypeMeta: metav1.TypeMeta{
				APIVersion: v1.SchemeGroupVersion.String(),
				Kind:       "ServiceAccount",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      saName,
				Namespace: app.Namespace,
			},
		})
	}
	podSpec.ServiceAccountName = saName

	return append(objects,
		&rbacv1.Role{
			TypeMeta: metav1.TypeMeta{
				APIVersion: rbacv1.SchemeGroupVersion.String(),
				Kind:       "Role",
			},
			ObjectMeta: metav1.ObjectMeta{Name: saName},
			Rules:      rules,
		},
		&rbacv1.RoleBinding{
			TypeMeta: metav1.TypeMeta{
				APIVersion: rbacv1.SchemeGroupVersion.String(),
				Kind:       "RoleBinding",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: saName,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      saName,
					Namespace: app.Namespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     saName,
			},
		}), nil
}

// generateEndpoints generates services and ingress resources for the App.
//
// It augments the provided podSpec, adding stunnel containers and volumes for
// each endpoint.
func generateEndpoints(spaceSpec *deccov1beta2.SpaceSpec, app *deccov1beta2.App, podSpec *v1.PodSpec, stunnelIndex *int) ([]runtime.Object, error) {
	var objects []runtime.Object
	if app.Spec.RunAsJob {
		return nil, nil // no service endpoints for a job
	}
	for _, e := range app.Spec.Endpoints {
		var err error
		var svcPort, tgtPort int32
		svcPort, tgtPort, err = createStunnel(spaceSpec, app, &e, podSpec, stunnelIndex)
		if err != nil {
			f := "failed to create stunnel for endpoint '%s': %s"
			return nil, fmt.Errorf(f, e.Name, err)
		}

		if obj := generateService(app, &e, svcPort, tgtPort); obj != nil {
			objects = append(objects, obj)
		}
		if obj := generateHTTPIngress(spaceSpec, app, &e); obj != nil {
			objects = append(objects, obj)
		}
		if obj := generateTCPIngress(spaceSpec, app, &e, svcPort); obj != nil {
			objects = append(objects, obj)
		}
	}
	return objects, nil
}

// createStunnel augments the podSpec, adding a Stunnel container and volume.
func createStunnel(
	spaceSpec *deccov1beta2.SpaceSpec,
	app *deccov1beta2.App,
	e *deccov1beta2.EndpointSpec,
	podSpec *v1.PodSpec,
	stunnelIndex *int,
) (
	svcPort int32,
	tgtPort int32,
	err error,
) {

	verifyChain := "yes"
	tlsSecretName := ""
	isNginxIngressStyleCertSecret := false

	svcPort = e.Port
	tgtPort = e.Port
	if tgtPort < 1 {
		err = deccov1beta2.ErrInvalidPort
		return
	}

	// Determine if we need ingress TLS termination
	if e.IsMetricsEndpoint {
		// Metrics endpoint. No TLS for now until I figure out how
		// to configure Prometheus to scrape using https -leb
		return
	} else if e.HttpPath == "" {
		// This is a TCP service.
		if e.DisableTlsTermination {
			// No stunnel needed
		} else {
			tlsSecretName = e.CertAndCaSecretName
			if tlsSecretName == "" {
				tlsSecretName = spaceSpec.TcpCertAndCaSecretName
				if tlsSecretName == "" {
					err = fmt.Errorf("space does not have cert for TCP service")
					return
				}
			}
			if e.DisableTcpClientTlsVerification {
				verifyChain = "no"
			}
		}
	} else if spaceSpec.EncryptHttp {
		// This is an encrypted HTTP service.
		tlsSecretName = e.CertAndCaSecretName
		if tlsSecretName == "" {
			tlsSecretName = spaceSpec.HttpCertSecretName
			if tlsSecretName == "" {
				err = fmt.Errorf("space does not have cert for HTTP service")
				return
			}
			// for now, we don't verify clients when using the space default
			// cert because it was most likely designed for web browser clients
			// which typically don't send a client cert.
			// FIXME: use default TCP cert instead with mutual authentication
			//        for connections b/w ingress controller and service
			verifyChain = "no"
		}
		isNginxIngressStyleCertSecret = true
	}

	if tlsSecretName != "" {
		svcPort = k8sutil.TlsPort
		destHostAndPort := fmt.Sprintf("%d", tgtPort)
		tgtPort = e.TlsListenPort
		if tgtPort == 0 {
			basePort := app.Spec.FirstEndpointListenPort
			if basePort == 0 {
				basePort = k8sutil.TlsPort
			}
			tgtPort = basePort + int32(*stunnelIndex)
		}
		containerName := fmt.Sprintf("stunnel-ingress-%d", *stunnelIndex)
		k8sutil.AddStunnelToPod(containerName, tgtPort, verifyChain, destHostAndPort,
			"", tlsSecretName, isNginxIngressStyleCertSecret,
			false, podSpec, 0, *stunnelIndex)
		*stunnelIndex += 1
	}
	return
}

func generateService(
	app *deccov1beta2.App,
	e *deccov1beta2.EndpointSpec,
	svcPort int32,
	tgtPort int32,
) *v1.Service {
	svcName := e.Name
	portName := svcName
	labels := map[string]string{
		"decco-derived-from": "app",
		"decco-app":          app.Name,
	}
	if e.IsMetricsEndpoint {
		labels["monitoring-group"] = "decco"
		portName = "metrics"
	}
	return &v1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1.SchemeGroupVersion.String(),
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:   svcName,
			Labels: labels,
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Port: svcPort,
					Name: portName,
					TargetPort: intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: tgtPort,
					},
				},
			},
			Selector: map[string]string{
				"decco-app": app.Name,
			},
		},
	}
}

func generateHTTPIngress(spaceSpec *deccov1beta2.SpaceSpec, app *deccov1beta2.App, e *deccov1beta2.EndpointSpec) *v1beta1.Ingress {
	if app.Spec.RunAsJob {
		return nil
	}

	path := e.HttpPath
	if path == "" {
		return nil
	}
	port := e.Port
	if spaceSpec.EncryptHttp {
		port = k8sutil.TlsPort
	}
	ingName := e.Name
	hostName := fmt.Sprintf("%s.%s", app.Namespace, spaceSpec.DomainName)
	secName := e.CertAndCaSecretName
	if secName == "" {
		secName = spaceSpec.HttpCertSecretName
	}
	return k8sutil.NewHttpIngress(
		app.Namespace,
		ingName,
		map[string]string{
			"decco-derived-from": "app",
			"decco-app":          app.Name,
		},
		hostName,
		path,
		e.Name,
		port,
		e.RewritePath,
		spaceSpec.EncryptHttp,
		secName,
		e.HttpLocalhostOnly,
		e.AdditionalIngressAnnotations,
	)
}

func generateTCPIngress(
	spaceSpec *deccov1beta2.SpaceSpec,
	app *deccov1beta2.App,
	e *deccov1beta2.EndpointSpec,
	svcPort int32,
) *v1beta1.Ingress {
	if e.IsMetricsEndpoint {
		return nil
	}
	path := e.HttpPath
	if path != "" {
		return nil
	}
	hostName := e.SniHostname
	if hostName == "" {
		hostName = e.Name + e.TcpHostnameSuffix + "." +
			app.Namespace + "." + spaceSpec.DomainName
	}
	anno := make(map[string]string)
	// Copy additional annotations. Note: this works if the source map is nil
	for key, val := range e.AdditionalIngressAnnotations {
		anno[key] = val
	}
	anno["kubernetes.io/ingress.class"] = "k8sniff"
	return &v1beta1.Ingress{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1beta1.SchemeGroupVersion.String(),
			Kind:       "Ingress",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: e.Name,
			Labels: map[string]string{
				"decco-derived-from": "app",
				"decco-app":          app.Name,
			},
			Annotations: anno,
		},
		Spec: v1beta1.IngressSpec{
			Rules: []v1beta1.IngressRule{
				{
					Host: hostName,
					IngressRuleValue: v1beta1.IngressRuleValue{
						HTTP: &v1beta1.HTTPIngressRuleValue{
							Paths: []v1beta1.HTTPIngressPath{
								{
									Backend: v1beta1.IngressBackend{
										ServiceName: e.Name,
										ServicePort: intstr.IntOrString{
											Type:   intstr.Int,
											IntVal: svcPort,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func generateDeploymentOrJob(
	spaceSpec *deccov1beta2.SpaceSpec,
	app *deccov1beta2.App,
	podSpec *v1.PodSpec,
	stunnelIndex int,
) (runtime.Object, error) {

	initialReplicas := app.Spec.InitialReplicas

	// egress TLS initiation
	for i, egress := range app.Spec.Egresses {
		clientTlsSecretName := egress.CertAndCaSecretName
		if clientTlsSecretName == "" {
			clientTlsSecretName = spaceSpec.TcpCertAndCaSecretName
			if clientTlsSecretName == "" {
				return nil, fmt.Errorf("tls secret not specified and there is no default for the space")
			}
		}
		containerName := fmt.Sprintf("stunnel-egress-%d", i)
		destHost := egress.Fqdn
		if destHost == "" {
			endpoint := egress.Endpoint
			if endpoint == "" {
				return nil, fmt.Errorf("tlsEgress entry: Fqdn and Endpoint cannot both be empty")
			}
			spaceName := egress.SpaceName
			if spaceName == "" {
				spaceName = app.Namespace
			}
			destHost = fmt.Sprintf("%s.%s.svc.cluster.local",
				endpoint, spaceName)
		}
		targetPort := egress.TargetPort
		if targetPort == 0 {
			targetPort = 443
		}
		destHostAndPort := fmt.Sprintf("%s:%d", destHost, targetPort)
		verifyChain := "yes"
		if egress.DisableServerCertVerification {
			verifyChain = "no"
		}
		k8sutil.AddStunnelToPod(
			containerName, egress.LocalPort, verifyChain,
			destHostAndPort, destHost,
			clientTlsSecretName, false, true,
			podSpec, egress.SpringBoardDelaySeconds, stunnelIndex,
		)
		stunnelIndex += 1
	}
	podTemplateSpec := v1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Name: app.Name,
			Labels: map[string]string{
				"app":       "decco",
				"decco-app": app.Name,
			},
		},
		Spec: *podSpec,
	}
	if app.Spec.RunAsJob {
		return &batchv1.Job{
			TypeMeta: metav1.TypeMeta{
				APIVersion: batchv1.SchemeGroupVersion.String(),
				Kind:       "Job",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      app.Name,
				Namespace: app.Namespace,
				Labels: map[string]string{
					"decco-derived-from": "app",
				},
			},
			Spec: batchv1.JobSpec{
				Template:     podTemplateSpec,
				BackoffLimit: &app.Spec.JobBackoffLimit,
			},
		}, nil
	} else {
		return &appsv1.Deployment{
			TypeMeta: metav1.TypeMeta{
				APIVersion: appsv1.SchemeGroupVersion.String(),
				Kind:       "Deployment",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      app.Name,
				Namespace: app.Namespace,
				Labels: map[string]string{
					"decco-derived-from": "app",
				},
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &initialReplicas,
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"decco-app": app.Name,
					},
				},
				Template: podTemplateSpec,
			},
		}, nil
	}
}
