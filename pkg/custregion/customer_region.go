package custregion

import (
	"github.com/sirupsen/logrus"
	"github.com/platform9/decco/pkg/spec"
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
	"k8s.io/apimachinery/pkg/api/resource"
)

type custRegRscEventType string

var (
	errInCreatingPhase = errors.New("custregion already in Creating phase")
)

const (
	eventDeleteCustomerRegion custRegRscEventType = "Delete"
	eventModifyCustomerRegion custRegRscEventType = "Modify"
)

type custRegRscEvent struct {
	typ     custRegRscEventType
	custRegRsc spec.CustomerRegion
}

type CustomerRegionRuntime struct {
	kubeApi kubernetes.Interface
	namespace string
	log *logrus.Entry

	//config Config

	crg spec.CustomerRegion

	// in memory state of the custRegRsc
	// status is the source of truth after CustomerRegionRuntime struct is materialized.
	status spec.CustomerRegionStatus
}

// -----------------------------------------------------------------------------

func New(
	crg spec.CustomerRegion,
	kubeApi kubernetes.Interface,
	namespace string,
) *CustomerRegionRuntime {

	lg := logrus.WithField("pkg","custregion",
		).WithField("custregion-name", crg.Name)

	c := &CustomerRegionRuntime{
		kubeApi:  kubeApi,
		log:      lg,
		crg:      crg,
		status:      crg.Status.Copy(),
		namespace: namespace,
	}

	if err := c.setup(); err != nil {
		c.log.Errorf("cluster failed to setup: %v", err)
		if c.status.Phase != spec.CustomerRegionPhaseFailed {
			c.status.SetReason(err.Error())
			c.status.SetPhase(spec.CustomerRegionPhaseFailed)
			if err := c.updateCRStatus(); err != nil {
				c.log.Errorf("failed to update custregion phase (%v): %v",
					spec.CustomerRegionPhaseFailed, err)
			}
		}
	}
	return c
}

// -----------------------------------------------------------------------------

func (c *CustomerRegionRuntime) Update(crg spec.CustomerRegion) {
}

// -----------------------------------------------------------------------------

func (c *CustomerRegionRuntime) Delete() {
	nsApi := c.kubeApi.CoreV1().Namespaces()
	err := nsApi.Delete(c.crg.Name, nil)
	if err != nil {
		c.log.Warn("failed to delete namespace %s: ", err.Error())
	}
}

// -----------------------------------------------------------------------------

func (c *CustomerRegionRuntime) updateCRStatus() error {
	if reflect.DeepEqual(c.crg.Status, c.status) {
		return nil
	}

	newCrg := c.crg
	newCrg.Status = c.status
	newCrg, err := k8sutil.UpdateCustomerRegionCustRsc(
		c.kubeApi.CoreV1().RESTClient(),
		c.namespace,
		newCrg)
	if err != nil {
		return fmt.Errorf("failed to update crg status: %v", err)
	}

	c.crg = newCrg
	return nil
}

// -----------------------------------------------------------------------------

func (c *CustomerRegionRuntime) setup() error {
	err := c.crg.Spec.Validate()
	if err != nil {
		return err
	}

	var shouldCreateResources bool
	switch c.status.Phase {
	case spec.CustomerRegionPhaseNone:
		shouldCreateResources = true
	case spec.CustomerRegionPhaseCreating:
		return errInCreatingPhase
	case spec.CustomerRegionPhaseActive:
		shouldCreateResources = false

	default:
		return fmt.Errorf("unexpected crg phase: %s", c.status.Phase)
	}

	if shouldCreateResources {
		return c.create()
	}
	return nil
}

// -----------------------------------------------------------------------------

func (c *CustomerRegionRuntime) phaseUpdateError(op string, err error) error {
	return fmt.Errorf(
		"%s : failed to update crg phase (%v): %v",
		op,
		c.status.Phase,
		err,
	)
}

// -----------------------------------------------------------------------------

func (c *CustomerRegionRuntime) create() error {
	c.status.SetPhase(spec.CustomerRegionPhaseCreating)
	if err := c.updateCRStatus(); err != nil {
		return c.phaseUpdateError("crg create", err)
	}
	c.logCreation()
	err := c.createNamespace()
	if err != nil {
		c.log.Warn("failed to create namespace for custreg %s: %s",
			c.crg.Name, err.Error())
		c.status.SetPhase(spec.CustomerRegionPhaseFailed)
		if err2 := c.updateCRStatus(); err2 != nil {
			return c.phaseUpdateError("crg create", err2)
		}
		return err
	}
	err = c.createHttpIngress()
	if err != nil {
		c.log.Warn("failed to create http ingress for custreg %s: %s",
			c.crg.Name, err.Error())
		c.status.SetPhase(spec.CustomerRegionPhaseFailed)
		if err2 := c.updateCRStatus(); err2 != nil {
			return c.phaseUpdateError("crg create", err2)
		}
		return err
	}
	err = c.createDefaultHttpDeploy()
	if err != nil {
		c.log.Warn("failed to create default http deployment for custreg %s: %s",
			c.crg.Name, err.Error())
		c.status.SetPhase(spec.CustomerRegionPhaseFailed)
		if err2 := c.updateCRStatus(); err2 != nil {
			return c.phaseUpdateError("crg create", err2)
		}
		return err
	}
	err = c.createDefaultHttpSvc()
	if err != nil {
		c.log.Warn("failed to create default http svc for custreg %s: %s",
			c.crg.Name, err.Error())
		c.status.SetPhase(spec.CustomerRegionPhaseFailed)
		if err2 := c.updateCRStatus(); err2 != nil {
			return c.phaseUpdateError("crg create", err2)
		}
		return err
	}
	c.status.SetPhase(spec.CustomerRegionPhaseActive)
	if err := c.updateCRStatus(); err != nil {
		return fmt.Errorf(
			"crg create: failed to update crg phase (%v): %v",
			spec.CustomerRegionPhaseActive,
			err,
		)
	}
	c.log.Infof("customer region is now active")
	return nil
}

// -----------------------------------------------------------------------------

func (c *CustomerRegionRuntime) logCreation() {
	specBytes, err := json.MarshalIndent(c.crg.Spec, "", "    ")
	if err != nil {
		c.log.Errorf("failed to marshal cluster spec: %v", err)
		return
	}

	c.log.Info("creating customer region with Spec:")
	for _, m := range strings.Split(string(specBytes), "\n") {
		c.log.Info(m)
	}
}

// -----------------------------------------------------------------------------

func (c *CustomerRegionRuntime) createNamespace() error {
	nsApi := c.kubeApi.CoreV1().Namespaces()
	ns := v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: c.crg.Name,
			Labels: map[string]string {
				"app": "decco",
			},
		},
	}
	_, err := nsApi.Create(&ns)
	return err
}

// -----------------------------------------------------------------------------

func (c *CustomerRegionRuntime) createHttpIngress() error {
	hostName := c.crg.Name + "." + c.crg.Spec.DomainName
	ingApi := c.kubeApi.ExtensionsV1beta1().Ingresses(c.crg.Name)
	ing := v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "http-ingress",
			Labels: map[string]string {
				"app": "decco",
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
									Path: "/",
									Backend: v1beta1.IngressBackend{
										ServiceName: "default-http",
										ServicePort: intstr.IntOrString {
											Type: intstr.Int,
											IntVal: 80,
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
					SecretName: c.crg.Spec.CertSecretName,
				},
			},
		},
	}
	_, err := ingApi.Create(&ing)
	return err
}

// -----------------------------------------------------------------------------

func (c *CustomerRegionRuntime) createDefaultHttpDeploy() error {
	depApi := c.kubeApi.ExtensionsV1beta1().Deployments(c.crg.Name)
	_, err := depApi.Create(&v1beta1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default-http",
			Labels: map[string]string {
				"app": "decco",
			},
		},
		Spec: v1beta1.DeploymentSpec{
			Replicas: nil,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string {
					"app": "default-http",
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta {
					Name: "default-http",
					Labels: map[string]string {
						"app": "default-http",
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container {
						{
							Name: "default-http",
							Image: "platform9systems/decco-default-http",
							Env: []v1.EnvVar{
								{
									Name: "MY_POD_NAMESPACE",
									ValueFrom: &v1.EnvVarSource{
										FieldRef: &v1.ObjectFieldSelector{
											FieldPath: "metadata.namespace",
										},
									},
								},
							},
							LivenessProbe: &v1.Probe{
								Handler: v1.Handler {
									HTTPGet: &v1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.IntOrString {
											Type: intstr.Int,
											IntVal: 8081,
										},
										Scheme: "HTTP",
									},
								},
								InitialDelaySeconds: 30,
								TimeoutSeconds: 5,
							},
							Ports: []v1.ContainerPort {
								{ ContainerPort: 8081 },
							},
							Resources: v1.ResourceRequirements {
								Limits: v1.ResourceList{
									"cpu": resource.Quantity{
										Format: "10m",
									},
									"memory": resource.Quantity{
										Format: "20Mi",
									},
								},
								Requests: v1.ResourceList{
									"cpu": resource.Quantity{
										Format: "10m",
									},
									"memory": resource.Quantity{
										Format: "20Mi",
									},
								},
							},
						},
					},
				},
			},
		},
	})
	return err
}

// -----------------------------------------------------------------------------

func (c *CustomerRegionRuntime) createDefaultHttpSvc() error {
	svcApi := c.kubeApi.CoreV1().Services(c.crg.Name)
	_, err := svcApi.Create(&v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default-http",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Port: 80,
					TargetPort: intstr.IntOrString {
						Type: intstr.Int,
						IntVal: 8081,
					},
				},
			},
			Selector: map[string]string {
				"app": "default-http",
			},
		},
	})
	return err
}
