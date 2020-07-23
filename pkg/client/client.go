package client

import (
	"sync"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	deccov1beta2 "github.com/platform9/decco/api/v1beta2"
)

var (
	scheme   *runtime.Scheme
	schemeMu = &sync.Mutex{}
)

func GetScheme() *runtime.Scheme {
	schemeMu.Lock()
	defer schemeMu.Unlock()
	if scheme != nil {
		return scheme
	}

	scheme = &(*clientgoscheme.Scheme)

	err := deccov1beta2.AddToScheme(scheme)
	if err != nil {
		panic(err)
	}
	return scheme
}

// TODO: make this private so that we don't expose RESTClient once operator code uses this client instead of REST calls
func New(cfg *rest.Config) (*rest.RESTClient, error) {
	config := *cfg
	config.GroupVersion = &deccov1beta2.GroupVersion
	config.APIPath = "/apis"
	config.ContentType = runtime.ContentTypeJSON
	config.NegotiatedSerializer = serializer.NewCodecFactory(GetScheme())
	client, err := rest.UnversionedRESTClientFor(&config)
	if err != nil {
		return nil, err
	}

	return client, nil
}
