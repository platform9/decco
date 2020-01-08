package app

import (
	"context"
	"github.com/sirupsen/logrus"
	spec "github.com/platform9/decco/pkg/appspec"
	sspec "github.com/platform9/decco/pkg/spec"
	"github.com/platform9/decco/pkg/k8sutil"
	"reflect"
	"k8s.io/client-go/kubernetes"
	"k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	rbacv1 "k8s.io/api/rbac/v1"
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
	propPolicy := metav1.DeletePropagationBackground
	delOpts := metav1.DeleteOptions{PropagationPolicy: &propPolicy}
	ctx := context.Background()
	for _, e := range ar.app.Spec.Endpoints {
		svcApi := ar.kubeApi.CoreV1().Services(ar.namespace)
		err := svcApi.Delete(ctx, e.Name, nil)
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
	if ar.app.Spec.RunAsJob {
		batchApi := ar.kubeApi.BatchV1().Jobs(ar.namespace)
		err := batchApi.Delete(ctx, ar.app.Name, &delOpts)
		if err != nil {
			log.Warnf("failed to delete job: %s", err)
		}
	} else {
		deployApi := ar.kubeApi.ExtensionsV1beta1().Deployments(ar.namespace)
		err := deployApi.Delete(ctx, ar.app.Name, &delOpts)
		if err != nil {
			log.Warnf("failed to delete deployment: %s", err)
		}
	}
	ar.teardownPermissions()
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
	ar.insertDomainEnvVar(containers)
	err := ar.setupPermissions(&podSpec)
	if err != nil {
		return fmt.Errorf("failed to set up permissions: %s", err)
	}
	containers, volumes, err = ar.createEndpoints(containers, volumes)
	if err != nil {
		return fmt.Errorf("failed to create endpoints: %s", err)
	}
	err = ar.createDeployment(podSpec, containers, volumes)
	if  err != nil {
		return fmt.Errorf("failed to create deployment: %s", err)
	}
	return nil
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) insertDomainEnvVar(containers []v1.Container) {
	if ar.app.Spec.DomainEnvVarName == "" {
		return
	}
	for i, _ := range containers {
		containers[i].Env = append(containers[i].Env, v1.EnvVar{
			Name: ar.app.Spec.DomainEnvVarName,
			Value: ar.spaceSpec.DomainName,
		})
	}
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) setupPermissions(podSpec *v1.PodSpec) error {
	sp := ar.app.Spec
	rules := sp.Permissions
	if rules == nil || len(rules) == 0 {
		return nil
	}
	ctx := context.Background()
	saName := podSpec.ServiceAccountName
	if saName == "" {
		saName = ar.app.Name
		sa := v1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name: saName,
			},
		}
		saApi := ar.kubeApi.CoreV1().ServiceAccounts(ar.namespace)
		_, err := saApi.Create(ctx, &sa, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create svcaccount: %s", err)
		}
	}

	role := rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: saName},
		Rules: rules,
	}
	rolesApi := ar.kubeApi.RbacV1().Roles(ar.namespace)
	_, err := rolesApi.Create(ctx, &role, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create role: %s", err)
	}
	rb := rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: saName},
		Subjects: []rbacv1.Subject{
			{
				Kind: "ServiceAccount",
				Name: saName,
				Namespace: ar.namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind: "Role",
			Name: saName,
		},
	}
	rbApi := ar.kubeApi.RbacV1().RoleBindings(ar.namespace)
	_, err = rbApi.Create(ctx, &rb, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create role binding: %s", err)
	}
	podSpec.ServiceAccountName = saName
	return nil
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) teardownPermissions() {
	sp := ar.app.Spec
	rules := sp.Permissions
	if rules == nil || len(rules) == 0 {
		return
	}
	log := ar.log.WithField("func", "teardownPermissions")
	rbApi := ar.kubeApi.RbacV1().RoleBindings(ar.namespace)
	ctx := context.Background()
	err := rbApi.Delete(ctx, ar.app.Name, nil)
	if err != nil {
		log.Warnf("failed to delete role binding: %s", err)
	}
	rolesApi := ar.kubeApi.RbacV1().Roles(ar.namespace)
	err = rolesApi.Delete(ctx, ar.app.Name, nil)
	if err != nil {
		log.Warnf("failed to delete role: %s", err)
	}
	if ar.app.Spec.PodSpec.ServiceAccountName != "" {
		// service account already existed, we didn't create it
		return
	}
	saApi := ar.kubeApi.CoreV1().ServiceAccounts(ar.namespace)
	err = saApi.Delete(ctx, ar.app.Name, nil)
	if err != nil {
		log.Warnf("failed to delete svc account: %s", err)
	}
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) updateDns(e *spec.EndpointSpec, delete bool) error {
	if !e.CreateDnsRecord {
		return nil
	}
	if !dns.Enabled() {
		return fmt.Errorf("CreateDnsRecord set but no DNS provider exists")
	}
	ipOrHostname, isHostname, err := k8sutil.GetTcpIngressIpOrHostname(ar.kubeApi)
	if err != nil {
		return fmt.Errorf("failed to get TCP ingress IP or hostname: %s", err)
	}
	name := fmt.Sprintf("%s.%s", e.Name, ar.namespace)
	return dns.UpdateRecord(ar.spaceSpec.DomainName, name,
		ipOrHostname, isHostname, delete)
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

func (ar *AppRuntime) createDeployment(
	podSpec v1.PodSpec,
	containers []v1.Container,
	volumes []v1.Volume,
) error {

	initialReplicas := ar.app.Spec.InitialReplicas
	podSpec.Containers = containers
	podSpec.Volumes = volumes

	objMeta := metav1.ObjectMeta{
		Name: ar.app.Name,
		Labels: map[string]string {
			"decco-derived-from": "app",
		},
	}
	ctx := context.Background()
	if ar.app.Spec.RunAsJob {
		batchApi := ar.kubeApi.BatchV1().Jobs(ar.namespace)
		backoffLimit := ar.app.Spec.JobBackoffLimit
		_, err := batchApi.Create(ctx, &batchv1.Job{
			ObjectMeta: objMeta,
			Spec: batchv1.JobSpec{
				Template: v1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta {
						Name: ar.app.Name,
						Labels: map[string]string {
							"app": "decco",
							"decco-app": ar.app.Name,
						},
					},
					Spec: podSpec,
				},
				BackoffLimit: &backoffLimit,
			},
		}, metav1.CreateOptions{})
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
				Template: v1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta {
						Name: ar.app.Name,
						Annotations: map[string]string {
							"linkerd.io/inject": "enabled",
							// https://linkerd.io/2/features/protocol-detection/#configuring-protocol-detection
							// Skip linkerd proxy when making outbound connections to mysql
							"config.linkerd.io/skip-outbound-ports": "3306",
						},
						Labels: map[string]string {
							"app": "decco",
							"decco-app": ar.app.Name,
						},
					},
					Spec: podSpec,
				},
			},
		}
		depApi := ar.kubeApi.ExtensionsV1beta1().Deployments(ar.namespace)
		_, err := depApi.Create(ctx, depSpec, metav1.CreateOptions{})
		return err
	}
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) createSvc(
	e *spec.EndpointSpec,
) error {
	appName := ar.Name()
	svcName := e.Name
	svcApi := ar.kubeApi.CoreV1().Services(ar.namespace)
	labels := map[string]string {
		"decco-derived-from": "app",
		"decco-app": appName,
	}
	if e.IsMetricsEndpoint {
		labels["monitoring-group"] = "decco"
	}
	ctx := context.Background()
	_, err := svcApi.Create(ctx, &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: svcName,
			Labels: labels,
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Port: 443,
					Name: "https",
					TargetPort: intstr.IntOrString {
						Type: intstr.Int,
						IntVal: e.Port,
					},
				},
				{
					Port: 80,
					Name: "http",
					TargetPort: intstr.IntOrString {
						Type: intstr.Int,
						IntVal: e.Port,
					},
				},
				{
					Port: e.Port,
					Name: "self",
					TargetPort: intstr.IntOrString {
						Type: intstr.Int,
						IntVal: e.Port,
					},
				},
			},
			Selector: map[string]string {
				"decco-app": appName,
			},
		},
	}, metav1.CreateOptions{})
	return err
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) createEndpoints(
	containers []v1.Container,
	volumes []v1.Volume,
) ([]v1.Container, []v1.Volume, error) {
	if ar.app.Spec.RunAsJob {
		return containers, volumes, nil // no service endpoints for a job
	}
	for _, e := range ar.app.Spec.Endpoints {
		var err error
		if err := ar.createSvc(&e); err != nil {
			f := "failed to create service for endpoint '%s': %s"
			return nil, nil, fmt.Errorf(f, e.Name, err)
		}
		// TODO: Create an ambassador Mapping CR instead of Ingress
		// if err := ar.createHttpIngress(&e); err != nil {
		// 	f := "failed to create http ingress for endpoint '%s': %s"
		// 	return nil, nil, fmt.Errorf(f, e.Name, err)
		// }
		// TODO: Create an ambassador Mapping/TCPMapping CR instead of Ingress w/k8sniff annotation
		// Haven't quite figured this out...
		// if err := ar.createTcpIngress(&e); err != nil {
		// 	f := "failed to create tcp ingress for endpoint '%s': %s"
		// 	return nil, nil, fmt.Errorf(f, e.Name, err)
		// }
		err = ar.updateDns(&e, false);
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
		e.RewritePath,
		ar.spaceSpec.EncryptHttp,
		secName,
		e.HttpLocalhostOnly,
		e.AdditionalIngressAnnotations,
	)
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) deleteIngress(e *spec.EndpointSpec) error {
	ingApi := ar.kubeApi.ExtensionsV1beta1().Ingresses(ar.namespace)
	ingName := e.Name
	ctx := context.Background()
	return ingApi.Delete(ctx, ingName, &metav1.DeleteOptions{})
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) createTcpIngress(
	e *spec.EndpointSpec,
	svcPort int32,
) error {
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
			ar.namespace + "." + ar.spaceSpec.DomainName
	}
	anno := make(map[string]string)
	// Copy additional annotations. Note: this works if the source map is nil
	for key, val := range e.AdditionalIngressAnnotations {
		anno[key] = val
	}
	anno["kubernetes.io/ingress.class"] = "k8sniff"
	ingApi := ar.kubeApi.ExtensionsV1beta1().Ingresses(ar.namespace)
	ing := v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: e.Name,
			Labels: map[string]string {
				"decco-derived-from": "app",
				"decco-app": ar.Name(),
			},
			Annotations: anno,
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
	ctx := context.Background()
	_, err := ingApi.Create(ctx, &ing, metav1.CreateOptions{})
	return err
}
