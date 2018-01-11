package watcher

import (
	"net/http"
	"github.com/sirupsen/logrus"
	"time"
	"fmt"
	"encoding/json"
	"errors"
	"io"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwatch "k8s.io/apimachinery/pkg/watch"
	"os"
	"strconv"
)

var (
	initRetryWaitTime = 30 * time.Second
	delayAfterWatchStreamCloseInSeconds = 2
	ErrTerminated = errors.New("gracefully terminated")
)

type WatchConsumer interface {
	Resync() (watchVersion string, err error)
	PeriodicTask()
	StartWatchRequest(watchVersion string) (*http.Response, error)
	UnmarshalEvent(evType kwatch.EventType, data []byte) (val interface{}, err error)
	HandleEvent(interface{}) (objVersion string, err error)
}

type WatchLoop interface {
	Run() error
}

type watchLoop struct {
	log *logrus.Entry
	cons WatchConsumer
	stopCh chan interface{}
}

type rawEvent struct {
	Type   kwatch.EventType
	Object json.RawMessage
}

func init() {
	d := os.Getenv("DELAY_AFTER_WATCH_STREAM_CLOSE_IN_SECONDS")
	if d != "" {
		if n, err := strconv.Atoi(d); err == nil {
			delayAfterWatchStreamCloseInSeconds = n
			logrus.Infof("delayAfterWatchStreamCloseInSeconds: %d",
				delayAfterWatchStreamCloseInSeconds)
		}
	}
}

func CreateWatchLoop(
	name string,
	c WatchConsumer,
	stopCh chan interface{},
) WatchLoop {
	return &watchLoop{
		log: logrus.WithField("watchname", name),
		cons: c,
		stopCh: stopCh,
	}
}

func (wl *watchLoop) Run() error {
	var watchVersion string
	var err error
	for {
		watchVersion, err = wl.cons.Resync()
		if err == nil {
			wl.log.Infof("watch loop started with initial version %s ",
				watchVersion)
			err = wl.watch(watchVersion)
			if err != nil {
				return err
			}
			wl.log.Infof("controller soft restart due to watch closure")
		} else {
			wl.log.Errorf("resync failed: %v", err)
			wl.log.Infof("retry in %v...", initRetryWaitTime)
			time.Sleep(initRetryWaitTime)
		}
	}
}

func (wl *watchLoop) watch(watchVersion string) error {

	for {
		wl.cons.PeriodicTask()
		resp, err := wl.cons.StartWatchRequest(watchVersion)
		wl.log.Infof("start watching at %v", watchVersion)

		if err != nil {
			return err
		}

		watchVersion, err = wl.processWatchResponse(watchVersion, resp)
		if err != nil {
			return err
		}
		if watchVersion == "" {
			// a watch closure after a DELETED event, requires a resync
			return nil
		}
	}
}

// ----------------------------------------------------------------------------

type eventChunk struct {
	isDelete bool
	ev interface{}
	st *metav1.Status
	err error
}

// ----------------------------------------------------------------------------

func (wl *watchLoop) decodeOneChunk(decoder *json.Decoder) <- chan eventChunk {
	evChan := make(chan eventChunk, 1)
	go func() {
		isDelete, ev, st, err := wl.pollEvent(decoder)
		evChan <- eventChunk{ isDelete,ev, st, err}
	} ()
	return evChan
}

// ----------------------------------------------------------------------------


func (wl *watchLoop) processWatchResponse(
	initialWatchVersion string,
	resp *http.Response,
) (nextWatchVersion string, err error) {

	nextWatchVersion = initialWatchVersion
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("invalid status code: " + resp.Status)
	}

	decoder := json.NewDecoder(resp.Body)
	for {
		var chunk eventChunk
		select {
		case chunk = <- wl.decodeOneChunk(decoder):
			break
		case <- wl.stopCh:
			return "", ErrTerminated
		}
		isDelete, ev, st, err := chunk.isDelete, chunk.ev, chunk.st, chunk.err
		if err != nil {
			if err == io.EOF { // apiserver will close stream periodically
				wl.log.Infof("watch stream closed, retrying in %d secs...", delayAfterWatchStreamCloseInSeconds)
				time.Sleep(time.Duration(delayAfterWatchStreamCloseInSeconds) * time.Second)
				return nextWatchVersion, nil
			}
			wl.log.Errorf("received invalid event from watch API: %v", err)
			return "", err
		}

		if st != nil {
			err = fmt.Errorf("unexpected watch error: %v", st)
			return "", err
		}

		//logrus.Infof("next watch version: %s", nextWatchVersion)
		objVersion, err := wl.cons.HandleEvent(ev)
		if err != nil {
			wl.log.Warningf("event handler returned possible error: %v", err)
			return "", err
		}
		if isDelete {
			// The RV on the deleted resource is out of date.
			// We also don't know the most up to date RV to use for the next
			// watch call if this event is the last before the current stream
			// closes. This special value indicates that a soft restart
			// (reconciliation) is needed
			nextWatchVersion = ""
		} else {
			nextWatchVersion = objVersion
		}
	}
}

func (wl *watchLoop) pollEvent(decoder *json.Decoder) (bool, interface{}, *metav1.Status, error) {
	re := &rawEvent{}
	err := decoder.Decode(re)
	if err != nil {
		if err == io.EOF {
			return false, nil, nil, err
		}
		return false, nil, nil, fmt.Errorf("fail to decode raw event from apiserver (%v)", err)
	}

	if re.Type == kwatch.Error {
		status := &metav1.Status{}
		err = json.Unmarshal(re.Object, status)
		if err != nil {
			return false, nil, nil, fmt.Errorf("fail to decode (%s) into metav1.Status (%v)", re.Object, err)
		}
		return false, nil, status, nil
	}

	isDelete := re.Type == kwatch.Deleted
	ev, err := wl.cons.UnmarshalEvent(re.Type, re.Object)
	if err != nil {
		return isDelete, nil, nil, fmt.Errorf("fail to unmarshal Cluster object from data (%s): %v", re.Object, err)
	}
	return isDelete, ev, nil, nil
}