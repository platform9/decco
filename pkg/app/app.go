package app

import (
	"github.com/sirupsen/logrus"
	spec "github.com/platform9/decco/pkg/appspec"
	sspec "github.com/platform9/decco/pkg/spec"
	"github.com/platform9/decco/pkg/k8sutil"
	"reflect"
	"k8s.io/client-go/kubernetes"
	cgoCoreV1 "k8s.io/client-go/kubernetes/typed/core/v1"
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

	lg := logrus.WithField("pkg","app",
		).WithField("app", app.Name)

	ar := &AppRuntime{
		kubeApi:   kubeApi,
		log:       lg,
		app:       app,
		status:    app.Status.Copy(),
		namespace: namespace,
		spaceSpec: spaceSpec,
	}

	if setupErr := ar.setup(); setupErr != nil {
		lg.Errorf("app failed to setup: %v", setupErr)
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
		batchApi.Delete(ar.app.Name, &metav1.DeleteOptions{})
	} else {
		err := ar.deleteHttpIngress()
		if err != nil {
			log.Warnf("failed to delete http ingress: %s", err)
		}
		deployApi := ar.kubeApi.ExtensionsV1beta1().Deployments(ar.namespace)
		propPolicy := metav1.DeletePropagationBackground
		delOpts := metav1.DeleteOptions{PropagationPolicy: &propPolicy}
		err = deployApi.Delete(ar.app.Name, &delOpts)
		if err != nil {
			log.Warnf("failed to delete deployment: %s", err)
		}
		svcApi := ar.kubeApi.CoreV1().Services(ar.namespace)
		err = svcApi.Delete(ar.app.Name, nil)
		if err != nil {
			log.Warnf("failed to delete service: %s", err)
		}
	}
	err := ar.updateDns(true)
	if err != nil {
		log.Warnf("failed to clean up DNS: %s", err)
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
		return err
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
	if err := ar.createSvc(); err != nil {
		return fmt.Errorf("failed to create service: %s", err)
	}
	if err := ar.createDeployment(); err != nil {
		return fmt.Errorf("failed to create deployment: %s", err)
	}
	if err := ar.createHttpIngress(); err != nil {
		return fmt.Errorf("failed to create http ingress: %s", err)
	}
	if err := ar.createTcpIngress(); err != nil {
		return fmt.Errorf("failed to create TCP ingress: %s", err)
	}
	if err := ar.updateDns(false); err != nil {
		return fmt.Errorf("failed to update DNS: %s", err)
	}
	return nil
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) updateDns(delete bool) error {
	if !ar.app.Spec.CreateDnsRecord {
		return nil
	}
	if !dns.Enabled() {
		return fmt.Errorf("CreateDnsRecord set but no DNS provider exists")
	}
	ip, err := k8sutil.GetTcpIngressIp(ar.kubeApi)
	if err != nil {
		return fmt.Errorf("failed to get TCP ingress IP: %s", err)
	}
	name := fmt.Sprintf("%s.%s", ar.app.Name, ar.namespace)
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

func (ar *AppRuntime) createDeployment() error {
	podSpec := ar.app.Spec.PodSpec
	initialReplicas := ar.app.Spec.InitialReplicas
	containers := podSpec.Containers
	volumes := podSpec.Volumes
	verifyChain := "no"
	tlsSecretName := ""
	isNginxIngressStyleCertSecret := false
	stunnelIndex := 0

	// Determine if we need ingress TLS termination
	if ar.app.Spec.HttpUrlPath == "" {
		// This is a TCP service.
		tlsSecretName = ar.spaceSpec.TcpCertAndCaSecretName
		if tlsSecretName == "" {
			return fmt.Errorf("space does not have cert for TCP service")
		}
		if ar.app.Spec.VerifyTcpClientCert {
			verifyChain = "yes"
		}
	} else if ar.spaceSpec.EncryptHttp {
		// This is an encrypted HTTP service.
		tlsSecretName = ar.spaceSpec.HttpCertSecretName
		if tlsSecretName == "" {
			return fmt.Errorf("space does not have cert for HTTP service")
		}
		isNginxIngressStyleCertSecret = true
	}

	if !ar.app.Spec.RunAsJob && tlsSecretName != "" {
		port := spec.FirstContainerPort(podSpec)
		if port < 0 {
			return spec.ErrContainerInvalidPorts
		}
		destHostAndPort := fmt.Sprintf("%d", port)
		volumes, containers = k8sutil.InsertStunnel(
			"stunnel-ingress", k8sutil.TlsPort, verifyChain,
			destHostAndPort, "",
			tlsSecretName, isNginxIngressStyleCertSecret, false,
			volumes, containers,
			0, stunnelIndex,
		)
		stunnelIndex += 1
	}

	// egress TLS initiation
	for i, egress := range ar.app.Spec.TlsEgresses {
		clientTlsSecretName := egress.CertAndCaSecretName
		if clientTlsSecretName == "" {
			clientTlsSecretName = tlsSecretName
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

func (ar *AppRuntime) createSvc() error {
	if ar.app.Spec.RunAsJob {
		return nil // no service for a job
	}
	port := spec.FirstContainerPort(ar.app.Spec.PodSpec)
	appName := ar.app.Name
	svcApi := ar.kubeApi.CoreV1().Services(ar.namespace)
	if ar.app.Spec.HttpUrlPath == "" {
		// This is a TCP service.
		// Create a cleartext version of the service if necessary
		if ar.app.Spec.CreateClearTextSvc {
			clearTextSvcName := appName + "-cleartext"
			err := createSvcInternal(svcApi, clearTextSvcName, appName, port)
			if err != nil {
				return fmt.Errorf("failed to create cleartext svc:", err)
			}
		}
		// The main service routes to pod's stunnel container
		port = k8sutil.TlsPort
	} else if ar.spaceSpec.EncryptHttp {
		port = k8sutil.TlsPort
	}
	// create main service to use as target for ingress controller
	err := createSvcInternal(svcApi, appName, appName, port)
	return err
}


func createSvcInternal (svcApi cgoCoreV1.ServiceInterface,
	svcName string, appName string, port int32) error {

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
					Port: port,
					TargetPort: intstr.IntOrString {
						Type: intstr.Int,
						IntVal: port,
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

func (ar *AppRuntime) httpIngressName() string {
	return ar.app.Name
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) createHttpIngress() error {
	if ar.app.Spec.RunAsJob {
		return nil
	}
	path := ar.app.Spec.HttpUrlPath
	if path == "" {
		ar.log.Debug("app does not have http path")
		return nil
	}
	port := spec.FirstContainerPort(ar.app.Spec.PodSpec)
	if ar.spaceSpec.EncryptHttp {
		port = k8sutil.TlsPort
	}
	ingName := ar.httpIngressName()
	hostName := fmt.Sprintf("%s.%s", ar.namespace, ar.spaceSpec.DomainName)
	secName := ar.app.Spec.CertAndCaSecretName
	if secName == "" {
		secName = ar.spaceSpec.HttpCertSecretName
	}
	return k8sutil.CreateHttpIngress(
		ar.kubeApi,
		ar.namespace,
		ingName,
		map[string]string {"decco-derived-from": "app"},
		hostName,
		fmt.Sprintf("/%s", ar.app.Name),
		ar.app.Name,
		port,
		ar.app.Spec.PreserveUri,
		ar.spaceSpec.EncryptHttp,
		secName,
	)
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) deleteHttpIngress() error {
	urlPath := ar.app.Spec.HttpUrlPath
	if urlPath == "" {
		ar.log.Debug("app does not have http path")
		return nil
	}
	ingApi := ar.kubeApi.ExtensionsV1beta1().Ingresses(ar.namespace)
	ingName := ar.httpIngressName()
	return ingApi.Delete(ingName, &metav1.DeleteOptions{})
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) createTcpIngress() error {
	path := ar.app.Spec.HttpUrlPath
	if path != "" {
		return nil
	}
	hostName := ar.app.Name + "." + ar.namespace + "." + ar.spaceSpec.DomainName
	ingApi := ar.kubeApi.ExtensionsV1beta1().Ingresses(ar.namespace)
	ing := v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: ar.app.Name,
			Labels: map[string]string {
				"decco-derived-from": "app",
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
										ServiceName: ar.app.Name,
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
