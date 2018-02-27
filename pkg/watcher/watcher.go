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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

var (
	initRetryWaitTime = 30 * time.Second
	delayAfterWatchStreamCloseInSeconds = 2
	ErrTerminated = errors.New("gracefully terminated")
)

type WatchConsumer interface {
	PeriodicTask(itemMap map[string]ManagedItem)
	StartWatchRequest(watchVersion string) (*http.Response, error)
	UnmarshalItem(evType kwatch.EventType, data []byte) (item Item, rv string, err error)
	LogEvent(evType kwatch.EventType, item Item)
	InitCRD() error
	GetItemList() (resourceVersion string, items []Item, err error)
	InitItem(item Item) ManagedItem
	GetItemType() string
}

type WatchLoop interface {
	Run() error
}

type Item interface {
	Name() string
}

type ManagedItem interface {
	Item
	Delete()
	Update(Item)
	Stop()
}

type watchLoop struct {
	log *logrus.Entry
	cons WatchConsumer
	stopCh chan interface{}
	itemMap map[string]ManagedItem
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
		itemMap: make(map[string]ManagedItem),
	}
}

func (wl *watchLoop) Run() error {
	var watchVersion string
	var err error

	defer func() {
		iType := wl.cons.GetItemType()
		for _, item := range wl.itemMap {
			wl.log.Infof("stopping %s '%s'", iType, item.Name())
			item.Stop()
		}
	}()

	for {
		watchVersion, err = wl.resync()
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
		wl.cons.PeriodicTask(wl.itemMap)
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
	evType kwatch.EventType
	item Item
	rv string
	st *metav1.Status
	err error
}

// ----------------------------------------------------------------------------

func (wl *watchLoop) decodeOneChunk(decoder *json.Decoder) <- chan eventChunk {
	evChan := make(chan eventChunk, 1)
	go func() {
		evType, item, rv, st, err := wl.pollEvent(decoder)
		if err == nil && item == nil {
			wl.log.Warnf("decodeOneChunk: both err and item are nil")
		}
		evChan <- eventChunk{ evType,item, rv,st, err}
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
		evType, item, objVersion, st, err := chunk.evType, chunk.item, chunk.rv, chunk.st, chunk.err
		if err == nil && item == nil {
			wl.log.Warnf("processWatchResponse: both err and item are nil")
		}
		if err != nil {
			if err == io.EOF { // apiserver will close stream periodically
				wl.log.Infof("watch stream closed, retrying in %d secs...", delayAfterWatchStreamCloseInSeconds)
				time.Sleep(time.Duration(delayAfterWatchStreamCloseInSeconds) * time.Second)
				return nextWatchVersion, nil
			}
			wl.log.Errorf("watch API failed: %v", err)
			return "", err
		}

		if st != nil {
			err = fmt.Errorf("unexpected watch error: %v", st)
			return "", err
		}

		//logrus.Infof("next watch version: %s", nextWatchVersion)
		if item == nil {
			// Shouldn't happen in theory but has been seen once in practice.
			// It shouldn't happen because if item were nil, then err should
			// be non-nil, causing the function to exit earlier.
			// For now, log the value of err and let LogEvent() panic.
			wl.log.Errorf("watchResponse: unexpected nil item, err:% v", err)
		}
		wl.cons.LogEvent(evType, item)
		err = wl.handleEvent(evType, item)
		if err != nil {
			wl.log.Warningf("event handler returned possible error: %v", err)
			return "", err
		}
		if evType == kwatch.Deleted {
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

// ----------------------------------------------------------------------------

func (wl *watchLoop) handleEvent(evType kwatch.EventType, item Item) error {
	name := item.Name()
	iType := wl.cons.GetItemType()
	switch evType {
	case kwatch.Added:
		if _, ok := wl.itemMap[name]; ok {
			return fmt.Errorf("unsafe state. %s '%s' was registered" +
				" before but we received event %s", iType, name, evType)
		}
		wl.itemMap[name] = wl.cons.InitItem(item)
		wl.log.Printf("%s '%s' added. " +
			"There now %d %ss", iType, name, len(wl.itemMap), iType)

	case kwatch.Modified:
		mgItem := wl.itemMap[name]
		if mgItem == nil {
			return fmt.Errorf("unsafe state. %s '%s' was not" +
				" registered but we received event %s", iType, name, evType)
		}
		mgItem.Update(item)
		wl.log.Printf("%s '%s' updated. " +
			"There now %d %ss", iType, name, len(wl.itemMap), iType)

	case kwatch.Deleted:
		mgItem := wl.itemMap[name]
		if mgItem == nil {
			return fmt.Errorf("unsafe state. %s '%s' was not " +
				"registered but we received event %s", iType, name, evType)
		}
		mgItem.Delete()
		delete(wl.itemMap, name)
		wl.log.Printf("%s '%s' deleted. " +
			"There now %d %ss", iType, name, len(wl.itemMap), iType)
	}
	return nil
}

// ----------------------------------------------------------------------------


func (wl *watchLoop) pollEvent(decoder *json.Decoder,
) (evType kwatch.EventType, item Item, rv string, st *metav1.Status, err error) {

	re := &rawEvent{}
	err = decoder.Decode(re)
	if err != nil {
		if err != io.EOF {
			err = fmt.Errorf("fail to decode raw event from apiserver (%v)", err)
		}
		return
	}

	evType = re.Type
	if evType == kwatch.Error {
		err = fmt.Errorf("pollEvent: watch error with payload: %s", re.Object)
		return
	}
	item, rv, err = wl.cons.UnmarshalItem(evType, re.Object)
	if err != nil {
		err = fmt.Errorf(
			"failed to unmarshal event type %v from data (%s): %v",
			evType,
			re.Object,
			err,
		)
	}
	if err == nil && item == nil {
		wl.log.Warnf("pollEvent: both err and item are nil")
	}
	return
}

// ----------------------------------------------------------------------------

func (wl *watchLoop) resync() (string, error) {
	watchVersion := "0"
	err := wl.cons.InitCRD()
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			// CRD has been initialized before. We need to recover existing spaces.
			watchVersion, err = wl.reconcileItems()
			if err != nil {
				return "", err
			}
		} else {
			return "", fmt.Errorf("fail to create CRD: %v", err)
		}
	}
	return watchVersion, nil
}

// ----------------------------------------------------------------------------

func (wl *watchLoop) reconcileItems() (string, error) {
	log := wl.log.WithField("func", "reconcileItems")
	rv, items, err := wl.cons.GetItemList()
	if err != nil {
		return "", err
	}
	t := wl.cons.GetItemType()
	m := make(map[string]bool)
	log.Debugf("--- %s reconciliation begin ---", t)
	for _, item := range items {
		m[item.Name()] = true
	}
	for name, item := range wl.itemMap {
		if name != item.Name() {
			return "", fmt.Errorf("name mismatch: %s vs %s", name,
				item.Name())
		}
		if !m[name] {
			log.Infof("deleting %s %s during reconciliation", t, name)
			item.Delete()
			delete(wl.itemMap, name)
		}
	}
	for _, item := range items {
		_, present := wl.itemMap[item.Name()]
		if !present {
			wl.itemMap[item.Name()] = wl.cons.InitItem(item)
		}
	}
	log.Debugf("--- %s reconciliation end ---", t)
	return rv, nil
}
