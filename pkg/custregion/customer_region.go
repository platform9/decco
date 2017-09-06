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

func New(
	crg spec.CustomerRegion,
	kubeApi kubernetes.Interface,
	stopC <-chan struct{},
	wg *sync.WaitGroup,
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

func (c *CustomerRegionRuntime) Update(crg spec.CustomerRegion) {
	c.send(custRegRscEvent{
		typ:     eventModifyCustomerRegion,
		custRegRsc: crg,
	})
}

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
