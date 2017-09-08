package custregion

import (
	"github.com/sirupsen/logrus"
	"github.com/platform9/decco/pkg/spec"
	"github.com/platform9/decco/pkg/k8sutil"
	"reflect"
	"k8s.io/client-go/kubernetes"
	"fmt"
	"errors"
	"strings"
	"encoding/json"
	"time"
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
	log *logrus.Entry

	//config Config

	crg spec.CustomerRegion

	// in memory state of the custRegRsc
	// status is the source of truth after CustomerRegionRuntime struct is materialized.
	status spec.CustomerRegionStatus
	eventCh chan custRegRscEvent
	stopCh  chan struct{}
}

// -----------------------------------------------------------------------------

func New(
	crg spec.CustomerRegion,
	kubeApi kubernetes.Interface,
) *CustomerRegionRuntime {

	lg := logrus.WithField("pkg","custregion",
		).WithField("custregion-name", crg.Name)

	c := &CustomerRegionRuntime{
		kubeApi:  kubeApi,
		log:      lg,
		crg:      crg,
		eventCh:     make(chan custRegRscEvent, 100),
		stopCh:      make(chan struct{}),
		status:      crg.Status.Copy(),
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
	c.send(custRegRscEvent{
		typ:     eventModifyCustomerRegion,
		custRegRsc: crg,
	})
}

// -----------------------------------------------------------------------------

func (c *CustomerRegionRuntime) send(ev custRegRscEvent) {
	select {
	case c.eventCh <- ev:
		l, ecap := len(c.eventCh), cap(c.eventCh)
		if l > int(float64(ecap)*0.8) {
			c.log.Warningf("eventCh buffer is almost full [%d/%d]", l, ecap)
		}
	case <-c.stopCh:
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

func (c *CustomerRegionRuntime) create() error {
	c.status.SetPhase(spec.CustomerRegionPhaseCreating)
	if err := c.updateCRStatus(); err != nil {
		return fmt.Errorf(
			"crg create: failed to update crg phase (%v): %v",
			spec.CustomerRegionPhaseCreating,
			err,
		)
	}
	c.logCreation()
	time.Sleep(2 * time.Second)

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
