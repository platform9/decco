package custregion

import (
	"github.com/sirupsen/logrus"
	"github.com/platform9/decco/pkg/spec"
	"sync"
	"k8s.io/client-go/kubernetes"
)

type custRegRscEventType string

const (
	eventDeleteCustomerRegion custRegRscEventType = "Delete"
	eventModifyCustomerRegion custRegRscEventType = "Modify"
)

type custRegRscEvent struct {
	typ     custRegRscEventType
	custRegRsc spec.CustomerRegionRsc
}

type CustomerRegion struct {
	kubeApi kubernetes.Interface
	log *logrus.Entry

	//config Config

	crr spec.CustomerRegionRsc

	// in memory state of the custRegRsc
	// status is the source of truth after CustomerRegion struct is materialized.
	status spec.CustomerRegionStatus
	eventCh chan custRegRscEvent
	stopCh  chan struct{}
}

func New(
	crr spec.CustomerRegionRsc,
	kubeApi kubernetes.Interface,
	stopC <-chan struct{},
	wg *sync.WaitGroup,
) *CustomerRegion {
	lg := logrus.WithField("pkg","custregion",
		).WithField("custregion-name", crr.Name)

	c := &CustomerRegion{
		kubeApi:  kubeApi,
		log:      lg,
		crr:      crr,
		eventCh:     make(chan custRegRscEvent, 100),
		stopCh:      make(chan struct{}),
		status:      crr.Status.Copy(),
	}

	wg.Add(1)
	go func() {
		defer wg.Done()

		/*
			if err := c.setup(); err != nil {
				c.logger.Errorf("cluster failed to setup: %v", err)
				if c.status.Phase != spec.ClusterPhaseFailed {
					c.status.SetReason(err.Error())
					c.status.SetPhase(spec.ClusterPhaseFailed)
					if err := c.updateCRStatus(); err != nil {
						c.logger.Errorf("failed to update cluster phase (%v): %v", spec.ClusterPhaseFailed, err)
					}
				}
				return
			}
			c.run(stopC)
		*/
		}()

	return c
}

func (c *CustomerRegion) Update(crr spec.CustomerRegionRsc) {
	c.send(custRegRscEvent{
		typ:     eventModifyCustomerRegion,
		custRegRsc: crr,
	})
}

func (c *CustomerRegion) send(ev custRegRscEvent) {
	select {
	case c.eventCh <- ev:
		l, ecap := len(c.eventCh), cap(c.eventCh)
		if l > int(float64(ecap)*0.8) {
			c.log.Warningf("eventCh buffer is almost full [%d/%d]", l, ecap)
		}
	case <-c.stopCh:
	}
}
