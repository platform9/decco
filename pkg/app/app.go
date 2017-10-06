package app

import (
	"github.com/sirupsen/logrus"
	spec "github.com/platform9/decco/pkg/appspec"
	"github.com/platform9/decco/pkg/k8sutil"
	"reflect"
	"k8s.io/client-go/kubernetes"
	"k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	"fmt"
	"errors"
	"strings"
	"encoding/json"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type appEventType string

var (
	errInCreatingPhase = errors.New("custregion already in Creating phase")
)

const (
	eventDeleteApp appEventType = "Delete"
	eventModifyApp appEventType = "Modify"
)

type appEvent struct {
	typ     appEventType
	app spec.App
}

type AppRuntime struct {
	kubeApi kubernetes.Interface
	namespace string
	domainName string
	tcpCertAndCaSecretName string
	log *logrus.Entry

	//config Config

	app spec.App

	// in memory state of the app
	// status is the source of truth after AppRuntime struct is materialized.
	status spec.AppStatus
}

// -----------------------------------------------------------------------------

func New(
	app spec.App,
	kubeApi kubernetes.Interface,
	namespace string,
	domainName string,
	tcpCertAndCaSecretName string,
) (*AppRuntime, error) {

	lg := logrus.WithField("pkg","app",
		).WithField("app", app.Name)

	ar := &AppRuntime{
		kubeApi:  kubeApi,
		log:      lg,
		app:      app,
		status:      app.Status.Copy(),
		namespace: namespace,
		domainName: domainName,
		tcpCertAndCaSecretName: tcpCertAndCaSecretName,
	}

	if setupErr := ar.setup(); setupErr != nil {
		creationErr := fmt.Errorf("app failed to setup: %v", setupErr)
		if ar.status.Phase != spec.AppPhaseFailed {
			ar.status.SetReason(setupErr.Error())
			ar.status.SetPhase(spec.AppPhaseFailed)
			if err := ar.updateCRStatus(); err != nil {
				ar.log.Errorf("failed to update app phase (%v): %v",
					spec.AppPhaseFailed, err)
			}
		}
		return nil, creationErr
	}
	return ar, nil
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) Update(app spec.App) {
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) GetApp() spec.App {
	return ar.app
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) Delete() {
	err := ar.removePathFromHttpIngress()
	if err != nil {
		ar.log.Warn("failed to remove path from ingress: %s", err)
	}
	deployApi := ar.kubeApi.ExtensionsV1beta1().Deployments(ar.namespace)
	propPolicy := metav1.DeletePropagationBackground
	delOpts := metav1.DeleteOptions{PropagationPolicy: &propPolicy}
	err = deployApi.Delete(ar.app.Name, &delOpts)
	if err != nil {
		ar.log.Warn("failed to delete deployment: %s", err)
	}
	svcApi := ar.kubeApi.CoreV1().Services(ar.namespace)
	err = svcApi.Delete(ar.app.Name, nil)
	if err != nil {
		ar.log.Warn("failed to delete service: %s", err)
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
	err := ar.app.Spec.Validate(ar.tcpCertAndCaSecretName)
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
	if err := ar.addPathToHttpIngress(); err != nil {
		return fmt.Errorf("failed to add path to http ingress: %s", err)
	}
	if err := ar.createTcpIngress(); err != nil {
		return fmt.Errorf("failed to create TCP ingress: %s", err)
	}
	return nil
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
	port := ar.app.Spec.ContainerSpec.Ports[0].ContainerPort
	depApi := ar.kubeApi.ExtensionsV1beta1().Deployments(ar.namespace)
	var initialReplicas int32 = ar.app.Spec.InitialReplicas
	containers := []v1.Container {
		ar.app.Spec.ContainerSpec,
	}
	volumes := []v1.Volume{}
	if ar.app.Spec.HttpUrlPath == "" {
		if ar.tcpCertAndCaSecretName == "" {
			return fmt.Errorf("customer region does not have cert for TCP service")
		}
		verifyChain := "no"
		if ar.app.Spec.VerifyTcpClientCert {
			verifyChain = "yes"
		}
		// This is a TCP service. Needs an stunnel container
		volumes = append(volumes, v1.Volume{
			Name: "certs",
			VolumeSource: v1.VolumeSource{
				Secret: &v1.SecretVolumeSource{
					SecretName: ar.tcpCertAndCaSecretName,
				},
			},
		})
		containers = append(containers, v1.Container{
			Name: "stunnel",
			Image: "platform9systems/stunnel",
			Ports: []v1.ContainerPort{
				{
					ContainerPort: 443,
				},
			},
			Env: []v1.EnvVar {
				{
					Name: "STUNNEL_VERIFY_CHAIN",
					Value: verifyChain,
				},
				{
					Name: "STUNNEL_CONNECT",
					Value: fmt.Sprintf("%d", port),
				},
			},
			VolumeMounts: []v1.VolumeMount{
				{
					Name: "certs",
					ReadOnly: true,
					MountPath: "/etc/stunnel/certs",
				},
			},
		})
	}

	depSpec := &v1beta1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: ar.app.Name,
			Labels: map[string]string {
				"decco-derived-from": "app",
			},
		},
		Spec: v1beta1.DeploymentSpec{
			Replicas: &initialReplicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string {
					"decco-app": ar.app.Name,
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta {
					Name: ar.app.Name,
					Labels: map[string]string {
						"app": "decco",
						"decco-app": ar.app.Name,
					},
				},
				Spec: v1.PodSpec{
					Containers: containers,
					Volumes: volumes,
				},
			},
		},
	}
	_, err := depApi.Create(depSpec)
	return err
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) createSvc() error {
	port := ar.app.Spec.ContainerSpec.Ports[0].ContainerPort
	if ar.app.Spec.HttpUrlPath == "" {
		// This is a TCP service. Route to pod's stunnel container
		port = 443
	}
	svcApi := ar.kubeApi.CoreV1().Services(ar.namespace)
	_, err := svcApi.Create(&v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: ar.app.Name,
			Labels: map[string]string {
				"decco-derived-from": "app",
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
				"decco-app": ar.app.Name,
			},
		},
	})
	return err
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) addPathToHttpIngress() error {
	path := ar.app.Spec.HttpUrlPath
	if path == "" {
		ar.log.Debug("app does not have http path")
		return nil
	}
	ingApi := ar.kubeApi.ExtensionsV1beta1().Ingresses(ar.namespace)
	ing, err := ingApi.Get("http-ingress", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get http ingress: %s", err)
	}
	rules := ing.Spec.Rules
	if len(rules) != 1 {
		return fmt.Errorf("http-ingress has invalid number of rules: %d",
			len(rules))
	}
	paths := rules[0].IngressRuleValue.HTTP.Paths
	if len(paths) < 1 {
		return fmt.Errorf("http-ingress has no paths")
	}
	port := ar.app.Spec.ContainerSpec.Ports[0].ContainerPort
	paths = append(paths, v1beta1.HTTPIngressPath{
		Path: ar.app.Spec.HttpUrlPath,
		Backend: v1beta1.IngressBackend{
			ServiceName: ar.app.Name,
			ServicePort: intstr.IntOrString {
				Type: intstr.Int,
				IntVal: port,
			},
		},
	})	
	
	ing.Spec.Rules[0].IngressRuleValue.HTTP.Paths = paths
	ing, err = ingApi.Update(ing)
	if err != nil {
		return fmt.Errorf("failed to update http ingress: %s", err)
	}
	return nil
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) removePathFromHttpIngress() error {
	urlPath := ar.app.Spec.HttpUrlPath
	if urlPath == "" {
		ar.log.Debug("app does not have http path")
		return nil
	}
	ingApi := ar.kubeApi.ExtensionsV1beta1().Ingresses(ar.namespace)
	ing, err := ingApi.Get("http-ingress", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get http ingress: %s", err)
	}
	rules := ing.Spec.Rules
	if len(rules) != 1 {
		return fmt.Errorf("http-ingress has invalid number of rules: %d",
			len(rules))
	}
	paths := rules[0].IngressRuleValue.HTTP.Paths
	if len(paths) < 1 {
		return fmt.Errorf("http-ingress has no paths")
	}
	for i, path := range paths {
		if path.Path == urlPath {
			paths = append(paths[:i], paths[i+1:]...)
			rules[0].IngressRuleValue.HTTP.Paths = paths
			_, err = ingApi.Update(ing)
			return err
		}
	}
	return fmt.Errorf("path %s not found in http ingress", urlPath)
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) createTcpIngress() error {
	path := ar.app.Spec.HttpUrlPath
	if path != "" {
		return nil
	}
	hostName := ar.app.Name + "." + ar.namespace + "." + ar.domainName
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
											IntVal: 443,
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
