package client

import (
	"fmt"
	"k8s.io/client-go/rest"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"github.com/platform9/decco/pkg/spec"
	"github.com/platform9/decco/pkg/appspec"
	"k8s.io/apimachinery/pkg/runtime"
)


// TODO: make this private so that we don't expose RESTClient once operator code uses this client instead of REST calls
func New(cfg *rest.Config) (*rest.RESTClient, *runtime.Scheme, error) {
	crScheme := runtime.NewScheme()
	if err := spec.AddToScheme(crScheme); err != nil {
		return nil, nil, err
	}

	config := *cfg
	config.GroupVersion = &spec.SchemeGroupVersion
	config.APIPath = "/apis"
	config.ContentType = runtime.ContentTypeJSON
	config.NegotiatedSerializer = serializer.DirectCodecFactory{CodecFactory: serializer.NewCodecFactory(crScheme)}

	client, err := rest.RESTClientFor(&config)
	if err != nil {
		return nil, nil, err
	}

	return client, crScheme, nil
}

func NewAppClient(cfg *rest.Config) (*rest.RESTClient, error) {
	config := *cfg
	config.GroupVersion = &appspec.SchemeGroupVersion
	config.APIPath = "/apis"
	config.NegotiatedSerializer = serializer.DirectCodecFactory{
		CodecFactory: appspec.Codecs,
	}
	restCli, err := rest.RESTClientFor(&config)
	if err != nil {
		return nil, fmt.Errorf("failed to create app rest client: %s", err)
	}
	return restCli, nil
}
