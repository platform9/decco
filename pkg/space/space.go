package space

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

type spaceRscEventType string

var (
	errInCreatingPhase = errors.New("space already in Creating phase")
)

const (
	eventDeleteSpace spaceRscEventType = "Delete"
	eventModifySpace spaceRscEventType = "Modify"
)

type spaceRscEvent struct {
	typ     spaceRscEventType
	spaceRsc spec.Space
}

type SpaceRuntime struct {
	kubeApi kubernetes.Interface
	namespace string
	log *logrus.Entry

	//config Config

	spc spec.Space

	// in memory state of the spaceRsc
	// status is the source of truth after SpaceRuntime struct is materialized.
	status spec.SpaceStatus
}

// -----------------------------------------------------------------------------

func New(
	spc spec.Space,
	kubeApi kubernetes.Interface,
	namespace string,
) *SpaceRuntime {

	lg := logrus.WithField("pkg","space",
		).WithField("space-name", spc.Name)

	c := &SpaceRuntime{
		kubeApi:  kubeApi,
		log:      lg,
		spc:      spc,
		status:      spc.Status.Copy(),
		namespace: namespace,
	}

	if err := c.setup(); err != nil {
		c.log.Errorf("cluster failed to setup: %v", err)
		if c.status.Phase != spec.SpacePhaseFailed {
			c.status.SetReason(err.Error())
			c.status.SetPhase(spec.SpacePhaseFailed)
			if err := c.updateCRStatus(); err != nil {
				c.log.Errorf("failed to update space phase (%v): %v",
					spec.SpacePhaseFailed, err)
			}
		}
	}
	return c
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) Update(spc spec.Space) {
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) Delete() {
	nsApi := c.kubeApi.CoreV1().Namespaces()
	err := nsApi.Delete(c.spc.Name, nil)
	if err != nil {
		c.log.Warn("failed to delete namespace %s: ", err.Error())
	}
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) updateCRStatus() error {
	if reflect.DeepEqual(c.spc.Status, c.status) {
		return nil
	}

	newCrg := c.spc
	newCrg.Status = c.status
	newCrg, err := k8sutil.UpdateSpaceCustRsc(
		c.kubeApi.CoreV1().RESTClient(),
		c.namespace,
		newCrg)
	if err != nil {
		return fmt.Errorf("failed to update spc status: %v", err)
	}

	c.spc = newCrg
	return nil
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) setup() error {
	err := c.spc.Spec.Validate()
	if err != nil {
		return err
	}

	var shouldCreateResources bool
	switch c.status.Phase {
	case spec.SpacePhaseNone:
		shouldCreateResources = true
	case spec.SpacePhaseCreating:
		return errInCreatingPhase
	case spec.SpacePhaseActive:
		shouldCreateResources = false

	default:
		return fmt.Errorf("unexpected spc phase: %s", c.status.Phase)
	}

	if shouldCreateResources {
		return c.create()
	}
	return nil
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) phaseUpdateError(op string, err error) error {
	return fmt.Errorf(
		"%s : failed to update spc phase (%v): %v",
		op,
		c.status.Phase,
		err,
	)
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) create() error {
	c.status.SetPhase(spec.SpacePhaseCreating)
	if err := c.updateCRStatus(); err != nil {
		return c.phaseUpdateError("spc create", err)
	}
	if err := c.internalCreate(); err != nil {
		return err
	}
	c.status.SetPhase(spec.SpacePhaseActive)
	if err := c.updateCRStatus(); err != nil {
		return fmt.Errorf(
			"spc create: failed to update spc phase (%v): %v",
			spec.SpacePhaseActive,
			err,
		)
	}
	c.log.Infof("space is now active")
	return nil
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) internalCreate() error {
	httpCert, err := c.getHttpCert()
	if err != nil {
		return fmt.Errorf("failed to read http cert: %s", err)
	}
	tcpCertAndCa, err := c.getTcpCertAndCa()
	if err != nil {
		return fmt.Errorf("failed to read TCP cert and CA: %s", err)
	}
	if err = c.createNamespace(); err != nil {
		return fmt.Errorf("failed to create namespace: %s", err)
	}
	if err = c.copySecret(httpCert); err != nil {
		return fmt.Errorf("failed to copy http cert: %s", err)
	}
	if tcpCertAndCa != nil {
		if err = c.copySecret(tcpCertAndCa); err != nil {
			return fmt.Errorf("failed to copy tcp cert and CA: %s", err)
		}
	} 
	if err = c.createHttpIngress(); err != nil {
		return fmt.Errorf("failed to create http ingress: %s", err)
	}
	if err = c.createDefaultHttpDeploy(); err != nil {
		return fmt.Errorf("failed to create default http dep: %s", err)
	}
	if err = c.createDefaultHttpSvc(); err != nil {
		return fmt.Errorf("failed to create default http svc: %s", err)
	}
	return nil
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) logCreation() {
	specBytes, err := json.MarshalIndent(c.spc.Spec, "", "    ")
	if err != nil {
		c.log.Errorf("failed to marshal cluster spec: %v", err)
		return
	}

	c.log.Info("creating space with Spec:")
	for _, m := range strings.Split(string(specBytes), "\n") {
		c.log.Info(m)
	}
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) createNamespace() error {
	nsApi := c.kubeApi.CoreV1().Namespaces()
	ns := v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: c.spc.Name,
			Labels: map[string]string {
				"app": "decco",
			},
		},
	}
	_, err := nsApi.Create(&ns)
	return err
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) createHttpIngress() error {
	hostName := c.spc.Name + "." + c.spc.Spec.DomainName
	ingApi := c.kubeApi.ExtensionsV1beta1().Ingresses(c.spc.Name)
	ing := v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "http-ingress",
			Labels: map[string]string {
				"app": "decco",
			},
			Annotations: map[string]string {
				"ingress.kubernetes.io/rewrite-target": "/",
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
					SecretName: c.spc.Spec.HttpCertSecretName,
				},
			},
		},
	}
	_, err := ingApi.Create(&ing)
	return err
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) createDefaultHttpDeploy() error {
	depApi := c.kubeApi.ExtensionsV1beta1().Deployments(c.spc.Name)
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

func (c *SpaceRuntime) createDefaultHttpSvc() error {
	svcApi := c.kubeApi.CoreV1().Services(c.spc.Name)
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

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) getHttpCert() (*v1.Secret, error) {
	secrApi := c.kubeApi.CoreV1().Secrets(c.namespace)
	return secrApi.Get(c.spc.Spec.HttpCertSecretName, metav1.GetOptions{})
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) getTcpCertAndCa() (*v1.Secret, error) {
	if c.spc.Spec.TcpCertAndCaSecretName == "" {
		return nil, nil
	}
	secrApi := c.kubeApi.CoreV1().Secrets(c.namespace)
	return secrApi.Get(c.spc.Spec.TcpCertAndCaSecretName, metav1.GetOptions{})
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) copySecret(s *v1.Secret) error {
	newCertSecret := v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: s.Name,
		},
		Data: s.Data,
		StringData: s.StringData,
	}
	secrApi := c.kubeApi.CoreV1().Secrets(c.spc.Name)
	_, err := secrApi.Create(&newCertSecret)
	return err
}
