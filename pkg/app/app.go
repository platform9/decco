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
) *AppRuntime {

	lg := logrus.WithField("pkg","app",
		).WithField("app", app.Name)

	c := &AppRuntime{
		kubeApi:  kubeApi,
		log:      lg,
		app:      app,
		status:      app.Status.Copy(),
		namespace: namespace,
	}

	if err := c.setup(); err != nil {
		c.log.Errorf("app failed to setup: %v", err)
		if c.status.Phase != spec.AppPhaseFailed {
			c.status.SetReason(err.Error())
			c.status.SetPhase(spec.AppPhaseFailed)
			if err := c.updateCRStatus(); err != nil {
				c.log.Errorf("failed to update app phase (%v): %v",
					spec.AppPhaseFailed, err)
			}
		}
	}
	return c
}

// -----------------------------------------------------------------------------

func (c *AppRuntime) Update(app spec.App) {
}

// -----------------------------------------------------------------------------

func (c *AppRuntime) Delete() {
	deployApi := c.kubeApi.ExtensionsV1beta1().Deployments(c.namespace)
	err := deployApi.Delete(c.app.Name, nil)
	if err != nil {
		c.log.Warn("failed to delete deployment %s: ", err.Error())
	}
	svcApi := c.kubeApi.CoreV1().Services(c.namespace)
	err = svcApi.Delete(c.app.Name, nil)
	if err != nil {
		c.log.Warn("failed to delete service %s: ", err.Error())
	}
}

// -----------------------------------------------------------------------------

func (c *AppRuntime) updateCRStatus() error {
	if reflect.DeepEqual(c.app.Status, c.status) {
		return nil
	}

	newApp := c.app
	newApp.Status = c.status
	newApp, err := k8sutil.UpdateAppCustRsc(
		c.kubeApi.CoreV1().RESTClient(),
		c.namespace,
		newApp)
	if err != nil {
		return fmt.Errorf("failed to update app status: %v", err)
	}

	c.app = newApp
	return nil
}

// -----------------------------------------------------------------------------

func (c *AppRuntime) setup() error {
	err := c.app.Spec.Validate()
	if err != nil {
		return err
	}

	var shouldCreateResources bool
	switch c.status.Phase {
	case spec.AppPhaseNone:
		shouldCreateResources = true
	case spec.AppPhaseCreating:
		return errInCreatingPhase
	case spec.AppPhaseActive:
		shouldCreateResources = false

	default:
		return fmt.Errorf("unexpected app phase: %s", c.status.Phase)
	}

	if shouldCreateResources {
		return c.create()
	}
	return nil
}

// -----------------------------------------------------------------------------

func (c *AppRuntime) phaseUpdateError(op string, err error) error {
	return fmt.Errorf(
		"%s : failed to update app phase (%v): %v",
		op,
		c.status.Phase,
		err,
	)
}

// -----------------------------------------------------------------------------

func (c *AppRuntime) create() error {
	c.status.SetPhase(spec.AppPhaseCreating)
	if err := c.updateCRStatus(); err != nil {
		return c.phaseUpdateError("app create", err)
	}
	if err := c.internalCreate(); err != nil {
		return err
	}
	c.status.SetPhase(spec.AppPhaseActive)
	if err := c.updateCRStatus(); err != nil {
		return fmt.Errorf(
			"app create: failed to update app phase (%v): %v",
			spec.AppPhaseActive,
			err,
		)
	}
	c.log.Infof("app is now active")
	return nil
}

// -----------------------------------------------------------------------------

func (c *AppRuntime) internalCreate() error {
	if err := c.createSvc(); err != nil {
		return fmt.Errorf("failed to create service: %s", err)
	}
	if err := c.createDeployment(); err != nil {
		return fmt.Errorf("failed to create deployment: %s", err)
	}
	return nil
}

// -----------------------------------------------------------------------------

func (c *AppRuntime) logCreation() {
	specBytes, err := json.MarshalIndent(c.app.Spec, "", "    ")
	if err != nil {
		c.log.Errorf("failed to app spec: %v", err)
		return
	}

	c.log.Info("creating app with Spec:")
	for _, m := range strings.Split(string(specBytes), "\n") {
		c.log.Info(m)
	}
}

// -----------------------------------------------------------------------------

func (c *AppRuntime) createDeployment() error {
	depApi := c.kubeApi.ExtensionsV1beta1().Deployments(c.namespace)
	var initialReplicas int32 = 0
	_, err := depApi.Create(&v1beta1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: c.app.Name,
			Labels: map[string]string {
				"decco-derived-from": "app",
			},
		},
		Spec: v1beta1.DeploymentSpec{
			Replicas: &initialReplicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string {
					"decco-app": c.app.Name,
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta {
					Name: c.app.Name,
					Labels: map[string]string {
						"app": "decco",
						"decco-app": c.app.Name,
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container {
						c.app.Spec.ContainerSpec,
					},
				},
			},
		},
	})
	return err
}

// -----------------------------------------------------------------------------

func (c *AppRuntime) createSvc() error {
	port := c.app.Spec.ContainerSpec.Ports[0].ContainerPort
	svcApi := c.kubeApi.CoreV1().Services(c.namespace)
	_, err := svcApi.Create(&v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: c.app.Name,
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
				"decco-app": c.app.Name,
			},
		},
	})
	return err
}

