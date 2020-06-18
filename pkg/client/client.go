package client

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	deccov1beta2 "github.com/platform9/decco/api/v1beta2"
)

func init() {
	// Add Decco types to the default scheme
	err := deccov1beta2.AddToScheme(scheme.Scheme)
	if err != nil {
		panic(err)
	}
}

// TODO: make this private so that we don't expose RESTClient once operator code uses this client instead of REST calls
func New(cfg *rest.Config) (*rest.RESTClient, error) {
	config := *cfg
	config.GroupVersion = &deccov1beta2.GroupVersion
	config.APIPath = "/apis"
	config.ContentType = runtime.ContentTypeJSON
	config.NegotiatedSerializer = serializer.NewCodecFactory(scheme.Scheme).WithoutConversion()
	client, err := rest.UnversionedRESTClientFor(&config)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func NewAppClient(cfg *rest.Config) (*rest.RESTClient, error) {
	config := *cfg
	config.GroupVersion = &deccov1beta2.GroupVersion
	config.APIPath = "/apis"
	config.NegotiatedSerializer = serializer.NewCodecFactory(scheme.Scheme).WithoutConversion()
	restCli, err := rest.UnversionedRESTClientFor(&config)
	if err != nil {
		return nil, fmt.Errorf("failed to create app rest client: %s", err)
	}
	return restCli, nil
}
