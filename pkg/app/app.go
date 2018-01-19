package app

import (
	"github.com/sirupsen/logrus"
	spec "github.com/platform9/decco/pkg/appspec"
	sspec "github.com/platform9/decco/pkg/spec"
	"github.com/platform9/decco/pkg/k8sutil"
	"reflect"
	"k8s.io/client-go/kubernetes"
	"k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	batchv1 "k8s.io/api/batch/v1"
	"fmt"
	"errors"
	"strings"
	"encoding/json"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"github.com/platform9/decco/pkg/dns"
	"github.com/platform9/decco/pkg/watcher"
)

var (
	errInCreatingPhase = errors.New("space already in Creating phase")
)

type AppRuntime struct {
	kubeApi   kubernetes.Interface
	namespace string
	spaceSpec sspec.SpaceSpec
	log       *logrus.Entry
	app       spec.App

	// in memory state of the app
	// status is the source of truth after AppRuntime struct is materialized.
	status spec.AppStatus
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) Name() string {
	return ar.app.Name
}

// -----------------------------------------------------------------------------

func New(
	app spec.App,
	kubeApi kubernetes.Interface,
	namespace string,
	spaceSpec sspec.SpaceSpec,
) *AppRuntime {

	log := logrus.WithField("pkg","app",
		).WithField("app", app.Name,
		).WithField("space", namespace)

	ar := &AppRuntime{
		kubeApi:   kubeApi,
		log:       log,
		app:       app,
		status:    app.Status.Copy(),
		namespace: namespace,
		spaceSpec: spaceSpec,
	}

	if setupErr := ar.setup(); setupErr != nil {
		log.Errorf("app failed to setup: %v", setupErr)
		if ar.status.Phase != spec.AppPhaseFailed {
			ar.status.SetReason(setupErr.Error())
			ar.status.SetPhase(spec.AppPhaseFailed)
			if err := ar.updateCRStatus(); err != nil {
				ar.log.Errorf("failed to update app phase (%v): %v",
					spec.AppPhaseFailed, err)
			}
		}
	}
	return ar
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) Update(item watcher.Item) {
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) GetApp() spec.App {
	return ar.app
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) Stop() {
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) Delete() {
	log := ar.log.WithField("func", "Delete")
	if ar.app.Spec.RunAsJob {
		batchApi := ar.kubeApi.BatchV1().Jobs(ar.namespace)
		err := batchApi.Delete(ar.app.Name, &metav1.DeleteOptions{})
		if err != nil {
			log.Warnf("failed to delete job: %s", err)
		}
		return
	}
	for _, e := range ar.app.Spec.Endpoints {
		svcApi := ar.kubeApi.CoreV1().Services(ar.namespace)
		err := svcApi.Delete(e.Name, nil)
		if err != nil {
			log.Warnf("failed to delete service '%s': %s", e.Name, err)
		}
		err = ar.deleteIngress(&e)
		if err != nil {
			log.Warnf("failed to delete ingress '%s': %s", e.Name, err)
		}
		err = ar.updateDns(&e, true)
		if err != nil {
			log.Warnf("failed to delete dns record for '%s': %s",
				e.Name, err)
		}
	}
	deployApi := ar.kubeApi.ExtensionsV1beta1().Deployments(ar.namespace)
	propPolicy := metav1.DeletePropagationBackground
	delOpts := metav1.DeleteOptions{PropagationPolicy: &propPolicy}
	err := deployApi.Delete(ar.app.Name, &delOpts)
	if err != nil {
		log.Warnf("failed to delete deployment: %s", err)
	}
	// TCP (k8sniff) ingress resources will be
	// cleaned up by garbage collection
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) updateCRStatus() error {
	if reflect.DeepEqual(ar.app.Status, ar.status) {
		return nil
	}

	newApp := ar.app
	newApp.Status = ar.status
	newApp, err := k8sutil.UpdateAppCustRsc(
		ar.kubeApi.CoreV1().RESTClient(),
		ar.namespace,
		newApp)
	if err != nil {
		return fmt.Errorf("failed to update app status: %v", err)
	}

	ar.app = newApp
	return nil
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) setup() error {
	err := ar.app.Spec.Validate(ar.spaceSpec.TcpCertAndCaSecretName)
	if err != nil {
		return fmt.Errorf("app failed to validate: %s", err)
	}

	var shouldCreateResources bool
	switch ar.status.Phase {
	case spec.AppPhaseNone:
		shouldCreateResources = true
	case spec.AppPhaseCreating:
		return errInCreatingPhase
	case spec.AppPhaseActive:
		shouldCreateResources = false

	default:
		return fmt.Errorf("unexpected app phase: %s", ar.status.Phase)
	}
	if shouldCreateResources {
		return ar.create()
	}
	return nil
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) phaseUpdateError(op string, err error) error {
	return fmt.Errorf(
		"%s : failed to update app phase (%v): %v",
		op,
		ar.status.Phase,
		err,
	)
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) create() error {
	ar.status.SetPhase(spec.AppPhaseCreating)
	if err := ar.updateCRStatus(); err != nil {
		return ar.phaseUpdateError("app create", err)
	}
	if err := ar.internalCreate(); err != nil {
		return err
	}
	ar.status.SetPhase(spec.AppPhaseActive)
	if err := ar.updateCRStatus(); err != nil {
		return fmt.Errorf(
			"app create: failed to update app phase (%v): %v",
			spec.AppPhaseActive,
			err,
		)
	}
	ar.log.Infof("app is now active")
	return nil
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) internalCreate() error {
	podSpec := ar.app.Spec.PodSpec
	containers := podSpec.Containers
	volumes := podSpec.Volumes
	stunnelIndex := 0
	var err error
	containers, volumes, err = ar.createEndpoints(containers, volumes, &stunnelIndex)
	if err != nil {
		return fmt.Errorf("failed to create endpoints: %s", err)
	}
	err = ar.createDeployment(podSpec, containers, volumes, stunnelIndex)
	if  err != nil {
		return fmt.Errorf("failed to create deployment: %s", err)
	}
	return nil
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) updateDns(e *spec.EndpointSpec, delete bool) error {
	if !e.CreateDnsRecord {
		return nil
	}
	if !dns.Enabled() {
		return fmt.Errorf("CreateDnsRecord set but no DNS provider exists")
	}
	ip, err := k8sutil.GetTcpIngressIp(ar.kubeApi)
	if err != nil {
		return fmt.Errorf("failed to get TCP ingress IP: %s", err)
	}
	name := fmt.Sprintf("%s.%s", e.Name, ar.namespace)
	return dns.UpdateRecord(ar.spaceSpec.DomainName, name, ip, delete)
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) logCreation() {
	specBytes, err := json.MarshalIndent(ar.app.Spec, "", "    ")
	if err != nil {
		ar.log.Errorf("failed to app spec: %v", err)
		return
	}

	ar.log.Info("creating app with Spec:")
	for _, m := range strings.Split(string(specBytes), "\n") {
		ar.log.Info(m)
	}
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) createStunnel(
	e *spec.EndpointSpec,
	containers []v1.Container,
	volumes []v1.Volume,
	stunnelIndex *int,
) (outCntrs []v1.Container, outVols []v1.Volume, listenPort int32, err error) {

	outCntrs = containers
	outVols = volumes
	verifyChain := "no"
	tlsSecretName := ""
	isNginxIngressStyleCertSecret := false

	// Determine if we need ingress TLS termination
	if e.HttpPath == "" {
		// This is a TCP service.
		tlsSecretName = e.CertAndCaSecretName
		if tlsSecretName == "" {
			tlsSecretName = ar.spaceSpec.TcpCertAndCaSecretName
			if tlsSecretName == "" {
				err = fmt.Errorf("space does not have cert for TCP service")
				return
			}
		}
		if e.VerifyTcpClientCert {
			verifyChain = "yes"
		}
	} else if ar.spaceSpec.EncryptHttp {
		// This is an encrypted HTTP service.
		tlsSecretName = e.CertAndCaSecretName
		if tlsSecretName == "" {
			tlsSecretName = ar.spaceSpec.HttpCertSecretName
			if tlsSecretName == "" {
				err = fmt.Errorf("space does not have cert for HTTP service")
				return
			}
		}
		isNginxIngressStyleCertSecret = true
	}

	listenPort = e.Port
	if listenPort < 1 {
		err = spec.ErrInvalidPort
		return
	}
	if tlsSecretName != "" {
		destHostAndPort := fmt.Sprintf("%d", listenPort)
		listenPort = k8sutil.TlsPort + int32(*stunnelIndex)
		containerName := fmt.Sprintf("stunnel-ingress-%d", *stunnelIndex)
		outVols, outCntrs = k8sutil.InsertStunnel(
			containerName, listenPort, verifyChain,
			destHostAndPort, "",
			tlsSecretName, isNginxIngressStyleCertSecret, false,
			outVols, outCntrs,
			0, *stunnelIndex,
		)
		*stunnelIndex += 1
	}
	return
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) createDeployment(
	podSpec v1.PodSpec,
	containers []v1.Container,
	volumes []v1.Volume,
	stunnelIndex int,
) error {

	initialReplicas := ar.app.Spec.InitialReplicas

	// egress TLS initiation
	for i, egress := range ar.app.Spec.TlsEgresses {
		clientTlsSecretName := egress.CertAndCaSecretName
		if clientTlsSecretName == "" {
			clientTlsSecretName = ar.spaceSpec.TcpCertAndCaSecretName
			if clientTlsSecretName == "" {
				return fmt.Errorf("tls secret not specified and there is no default for the space")
			}
		}
		containerName := fmt.Sprintf("stunnel-egress-%d", i)
		destHost := egress.Fqdn
		if destHost == "" {
			appName := egress.AppName
			if appName == "" {
				return fmt.Errorf("tlsEgress entry: Fqdn and AppName cannot both be empty")
			}
			spaceName := egress.SpaceName
			if spaceName == "" {
				spaceName = ar.namespace
			}
			destHost = fmt.Sprintf("%s.%s.svc.cluster.local",
				appName, spaceName)
		}
		targetPort := egress.TargetPort
		if targetPort == 0 {
			targetPort = 443
		}
		destHostAndPort := fmt.Sprintf("%s:%d", destHost, targetPort)
		volumes, containers = k8sutil.InsertStunnel(
			containerName, egress.LocalPort, "yes",
			destHostAndPort, destHost,
			clientTlsSecretName, false, true,
			volumes, containers, egress.SpringBoardDelaySeconds, stunnelIndex,
		)
		stunnelIndex += 1
	}
	podSpec.Containers = containers
	podSpec.Volumes = volumes
	objMeta := metav1.ObjectMeta{
		Name: ar.app.Name,
		Labels: map[string]string {
			"decco-derived-from": "app",
		},
	}
	podTemplateSpec := v1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta {
			Name: ar.app.Name,
			Labels: map[string]string {
				"app": "decco",
				"decco-app": ar.app.Name,
			},
		},
		Spec: podSpec,
	}
	if ar.app.Spec.RunAsJob {
		batchApi := ar.kubeApi.BatchV1().Jobs(ar.namespace)
		_, err := batchApi.Create(&batchv1.Job{
			ObjectMeta: objMeta,
			Spec: batchv1.JobSpec{
				Template: podTemplateSpec,
			},
		})
		return err
	} else {
		depSpec := &v1beta1.Deployment{
			ObjectMeta: objMeta,
			Spec: v1beta1.DeploymentSpec{
				Replicas: &initialReplicas,
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string {
						"decco-app": ar.app.Name,
					},
				},
				Template: podTemplateSpec,
			},
		}
		depApi := ar.kubeApi.ExtensionsV1beta1().Deployments(ar.namespace)
		_, err := depApi.Create(depSpec)
		return err
	}
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) createSvc(e *spec.EndpointSpec, listenPort int32) error {
	appName := ar.Name()
	svcName := e.Name
	svcApi := ar.kubeApi.CoreV1().Services(ar.namespace)
	_, err := svcApi.Create(&v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: svcName,
			Labels: map[string]string {
				"decco-derived-from": "app",
				"decco-app": appName,
			},
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Port: k8sutil.TlsPort,
					TargetPort: intstr.IntOrString {
						Type: intstr.Int,
						IntVal: listenPort,
					},
				},
			},
			Selector: map[string]string {
				"decco-app": appName,
			},
		},
	})
	return err
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) createEndpoints(
	containers []v1.Container,
	volumes []v1.Volume,
	stunnelIndex *int,
) ([]v1.Container, []v1.Volume, error) {
	if ar.app.Spec.RunAsJob {
		return containers, volumes, nil // no service endpoints for a job
	}
	for _, e := range ar.app.Spec.Endpoints {
		var err error
		var listenPort int32
		containers, volumes, listenPort, err = ar.createStunnel(&e, containers,
			volumes, stunnelIndex);
		if err != nil {
			f := "failed to create stunnel for endpoint '%s': %s"
			return nil, nil, fmt.Errorf(f, e.Name, err)
		}
		if err := ar.createSvc(&e, listenPort); err != nil {
			f := "failed to create service for endpoint '%s': %s"
			return nil, nil, fmt.Errorf(f, e.Name, err)
		}
		if err := ar.createHttpIngress(&e); err != nil {
			f := "failed to create http ingress for endpoint '%s': %s"
			return nil, nil, fmt.Errorf(f, e.Name, err)
		}
		if err := ar.createTcpIngress(&e); err != nil {
			f := "failed to create tcp ingress for endpoint '%s': %s"
			return nil, nil, fmt.Errorf(f, e.Name, err)
		}
		err = ar.updateDns(&e,false);
		if err != nil {
			f := "failed to update dns for endpoint '%s': %s"
			return nil, nil, fmt.Errorf(f, e.Name, err)
		}
	}
	return containers, volumes, nil
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) createHttpIngress(e *spec.EndpointSpec) error {
	if ar.app.Spec.RunAsJob {
		return nil
	}
	path := e.HttpPath
	if path == "" {
		ar.log.Debug("endpoint does not have http path")
		return nil
	}
	port := e.Port
	if ar.spaceSpec.EncryptHttp {
		port = k8sutil.TlsPort
	}
	ingName := e.Name
	hostName := fmt.Sprintf("%s.%s", ar.namespace, ar.spaceSpec.DomainName)
	secName := e.CertAndCaSecretName
	if secName == "" {
		secName = ar.spaceSpec.HttpCertSecretName
	}
	return k8sutil.CreateHttpIngress(
		ar.kubeApi,
		ar.namespace,
		ingName,
		map[string]string {
			"decco-derived-from": "app",
			"decco-app": ar.Name(),
		},
		hostName,
		path,
		e.Name,
		port,
		e.PreservePath,
		ar.spaceSpec.EncryptHttp,
		secName,
	)
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) deleteIngress(e *spec.EndpointSpec) error {
	ingApi := ar.kubeApi.ExtensionsV1beta1().Ingresses(ar.namespace)
	ingName := e.Name
	return ingApi.Delete(ingName, &metav1.DeleteOptions{})
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) createTcpIngress(e *spec.EndpointSpec) error {
	path := e.HttpPath
	if path != "" {
		return nil
	}
	hostName := e.Name + "." + ar.namespace + "." + ar.spaceSpec.DomainName
	ingApi := ar.kubeApi.ExtensionsV1beta1().Ingresses(ar.namespace)
	ing := v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: e.Name,
			Labels: map[string]string {
				"decco-derived-from": "app",
				"decco-app": ar.Name(),
			},
			Annotations: map[string]string {
				"kubernetes.io/ingress.class": "k8sniff",
			},
		},
		Spec: v1beta1.IngressSpec{
			Rules: []v1beta1.IngressRule{
				{
					Host: hostName,
					IngressRuleValue: v1beta1.IngressRuleValue {
						HTTP: &v1beta1.HTTPIngressRuleValue{
							Paths: []v1beta1.HTTPIngressPath {
								{
									Backend: v1beta1.IngressBackend{
										ServiceName: e.Name,
										ServicePort: intstr.IntOrString {
											Type: intstr.Int,
											IntVal: k8sutil.TlsPort,
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
	_, err := ingApi.Create(&ing)
	return err
}
