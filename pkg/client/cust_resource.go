package client

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"

	deccov1 "github.com/platform9/decco/api/v1"
)

// TODO: make this private so that we don't expose RESTClient once operator code uses this client instead of REST calls
func New(cfg *rest.Config) (*rest.RESTClient, error) {
	config := *cfg
	config.GroupVersion = &deccov1.GroupVersion
	config.APIPath = "/apis"
	config.ContentType = runtime.ContentTypeJSON
	config.NegotiatedSerializer = serializer.NewCodecFactory(runtime.NewScheme())

	client, err := rest.RESTClientFor(&config)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func NewAppClient(cfg *rest.Config) (*rest.RESTClient, error) {
	config := *cfg
	config.GroupVersion = &deccov1.GroupVersion
	config.APIPath = "/apis"
	config.NegotiatedSerializer = serializer.NewCodecFactory(runtime.NewScheme())
	restCli, err := rest.RESTClientFor(&config)
	if err != nil {
		return nil, fmt.Errorf("failed to create app rest client: %s", err)
	}
	return restCli, nil
}
