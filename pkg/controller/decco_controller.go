package controller

import (
	"github.com/sirupsen/logrus"
)

func init() {
	logrus.Println("controller package initialized")
}

type Controller struct {
	log *logrus.Entry
}

func New() *Controller {
	return &Controller{
		log: logrus.WithField("pkg", "controller"),
	}
}

func (c *Controller) SayHi() {
	c.log.Println("Hi from controller")
}
