package client

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"

	"github.com/platform9/decco/pkg/appspec"
	"github.com/platform9/decco/pkg/spec"
)

// TODO: make this private so that we don't expose RESTClient once operator code uses this client instead of REST calls
func New(cfg *rest.Config) (*rest.RESTClient, error) {
	config := *cfg
	config.GroupVersion = &spec.SchemeGroupVersion
	config.APIPath = "/apis"
	config.ContentType = runtime.ContentTypeJSON
	config.NegotiatedSerializer = spec.Codecs.WithoutConversion()

	client, err := rest.RESTClientFor(&config)
	if err != nil {
		return nil, err
	}

	return client, nil
}

func NewAppClient(cfg *rest.Config) (*rest.RESTClient, error) {
	config := *cfg
	config.GroupVersion = &appspec.SchemeGroupVersion
	config.APIPath = "/apis"
	config.NegotiatedSerializer = appspec.Codecs.WithoutConversion()
	restCli, err := rest.RESTClientFor(&config)
	if err != nil {
		return nil, fmt.Errorf("failed to create app rest client: %s", err)
	}
	return restCli, nil
}
