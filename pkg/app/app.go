package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	deccov1beta2 "github.com/platform9/decco/api/v1beta2"
	"github.com/platform9/decco/pkg/dns"
	"github.com/platform9/decco/pkg/k8sutil"
	"github.com/platform9/decco/pkg/watcher"
)

var (
	errInCreatingPhase = errors.New("app already in Creating phase")
)

type AppRuntime struct {
	kubeApi   kubernetes.Interface
	namespace string
	spaceSpec deccov1beta2.SpaceSpec
	log       *logrus.Entry
	app       deccov1beta2.App

	// in memory state of the app
	// status is the source of truth after AppRuntime struct is materialized.
	status deccov1beta2.AppStatus
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) Name() string {
	return ar.app.Name
}

// -----------------------------------------------------------------------------

func New(
	app deccov1beta2.App,
	kubeApi kubernetes.Interface,
	namespace string,
	spaceSpec deccov1beta2.SpaceSpec,
) *AppRuntime {

	log := logrus.WithField("pkg", "app").WithField("app", app.Name).WithField("space", namespace)

	ar := &AppRuntime{
		kubeApi:   kubeApi,
		log:       log,
		app:       app,
		status:    *app.Status.DeepCopy(),
		namespace: namespace,
		spaceSpec: spaceSpec,
	}

	if setupErr := ar.setup(); setupErr != nil {
		log.Errorf("app failed to setup: %v", setupErr)
		if ar.status.Phase != deccov1beta2.AppPhaseFailed {
			ar.status.SetReason(setupErr.Error())
			ar.status.SetPhase(deccov1beta2.AppPhaseFailed)
			if err := ar.updateCRStatus(); err != nil {
				ar.log.Errorf("failed to update app phase (%v): %v",
					deccov1beta2.AppPhaseFailed, err)
			}
		}
	}
	return ar
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) Update(item watcher.Item) {
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) GetApp() deccov1beta2.App {
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
	}

	err := ar.configureDNS(true)
	if err != nil {
		log.Warnf("failed to delete dns records: %s", err)
	}

	if ar.app.Spec.RunAsJob {
		batchApi := ar.kubeApi.BatchV1().Jobs(ar.namespace)
		err := batchApi.Delete(ar.app.Name, &delOpts)
		if err != nil {
			log.Warnf("failed to delete job: %s", err)
		}
	} else {
		deployApi := ar.kubeApi.AppsV1().Deployments(ar.namespace)
		err := deployApi.Delete(ar.app.Name, &delOpts)
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
	case deccov1beta2.AppPhaseNone:
		shouldCreateResources = true
	case deccov1beta2.AppPhaseCreating:
		return errInCreatingPhase
	case deccov1beta2.AppPhaseActive:
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
	ar.status.SetPhase(deccov1beta2.AppPhaseCreating)
	if err := ar.updateCRStatus(); err != nil {
		return ar.phaseUpdateError("app create", err)
	}
	if err := ar.internalCreate(); err != nil {
		return err
	}
	ar.status.SetPhase(deccov1beta2.AppPhaseActive)
	if err := ar.updateCRStatus(); err != nil {
		return fmt.Errorf(
			"app create: failed to update app phase (%v): %v",
			deccov1beta2.AppPhaseActive,
			err,
		)
	}
	ar.log.Infof("app is now active")
	return nil
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) internalCreate() error {
	// Generate the Kubernetes resources
	objs, err := GenerateResources(&ar.spaceSpec, &ar.app)
	if err != nil {
		return err
	}

	// Attempt to create each of the generated resources
	// TODO(erwin) test if this works, otherwise revert to the dynamic client.
	for _, obj := range objs {
		err := ar.kubeApi.Discovery().RESTClient().Put().
			Namespace(ar.namespace).
			Resource(obj.GetObjectKind().GroupVersionKind().GroupVersion().String()).
			Body(obj).
			Do().
			Error()
		if err != nil {
			return fmt.Errorf("failed to create object %s: %w", obj.GetObjectKind().GroupVersionKind(), err)
		}
	}

	// Finally, configure the DNS for tha App.
	return ar.configureDNS(false)
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
	err := rbApi.Delete(ar.app.Name, nil)
	if err != nil {
		log.Warnf("failed to delete role binding: %s", err)
	}
	rolesApi := ar.kubeApi.RbacV1().Roles(ar.namespace)
	err = rolesApi.Delete(ar.app.Name, nil)
	if err != nil {
		log.Warnf("failed to delete role: %s", err)
	}
	if ar.app.Spec.PodSpec.ServiceAccountName != "" {
		// service account already existed, we didn't create it
		return
	}
	saApi := ar.kubeApi.CoreV1().ServiceAccounts(ar.namespace)
	err = saApi.Delete(ar.app.Name, nil)
	if err != nil {
		log.Warnf("failed to delete svc account: %s", err)
	}
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) updateDNSForEndpoint(e *deccov1beta2.EndpointSpec, delete bool) error {
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

func (ar *AppRuntime) configureDNS(delete bool) error {
	for _, e := range ar.app.Spec.Endpoints {
		err := ar.updateDNSForEndpoint(&e, delete)
		if err != nil {
			return err
		}
	}
	return nil
}

// -----------------------------------------------------------------------------

func (ar *AppRuntime) deleteIngress(e *deccov1beta2.EndpointSpec) error {
	ingApi := ar.kubeApi.NetworkingV1beta1().Ingresses(ar.namespace)
	ingName := e.Name
	return ingApi.Delete(ingName, &metav1.DeleteOptions{})
}
