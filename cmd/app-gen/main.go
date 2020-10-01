// Application app-gen generates Kubernetes resources from an App resource
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"

	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"sigs.k8s.io/yaml"

	deccov1beta2 "github.com/platform9/decco/api/v1beta2"
	"github.com/platform9/decco/pkg/app"
)

type Options struct {
	deccov1beta2.SpaceSpec
	SpaceResourcePath string
}

func main() {
	opts := &Options{}
	flag.StringVar(&opts.DomainName, "domain", "", "The base domain used for this App's Space. (equivalent: space.DomainName)")
	flag.StringVar(&opts.HttpCertSecretName, "httpCertSecretName", "", "The name of the HTTP TLS secret used for this App's Space. (equivalent: space.HttpCertSecretName)")
	flag.StringVar(&opts.TcpCertAndCaSecretName, "tcpCertAndCaSecretName", "", "The name of the TCP TLS secret used for this App's Space. (optional; equivalent: space.TcpCertAndCaSecretName)")
	flag.BoolVar(&opts.EncryptHttp, "encryptHttp", false, "if enabled, the ingress will use HTTPS instead of HTTP to communicate with the backend services. (optional; equivalent: space.EncryptHttp)")
	flag.StringVar(&opts.SpaceResourcePath, "space", "", "Path to a Space resource to use for the Space-related configuration of the App. If used, the other flags will be ignored.")
	flag.Parse()

	if flag.NArg() < 1 {
		printHelp()
		os.Exit(1)
	}
	appLocation := flag.Arg(0)

	// Complete the options
	if opts.SpaceResourcePath != "" {
		bs, err := ioutil.ReadFile(opts.SpaceResourcePath)
		if err != nil {
			log.Fatalf("Failed to read App resource at %s: %s", appLocation, err)
		}

		space := &deccov1beta2.Space{}
		err = yaml.Unmarshal(bs, space)
		if err != nil {
			log.Fatalf("Failed to parse Space from %s: %s", opts.SpaceResourcePath, err)
		}
		opts.SpaceSpec = space.Spec
	}

	// Validate the options
	if err := opts.SpaceSpec.Validate(); err != nil {
		printHelp()
		fmt.Println()
		log.Fatalf("Invalid Space.Spec provided: %s", err)
	}

	// Generate the App resources
	bs, err := ioutil.ReadFile(appLocation)
	if err != nil {
		log.Fatalf("Failed to read App resource at %s: %s", appLocation, err)
	}

	dapp := &deccov1beta2.App{}
	err = yaml.Unmarshal(bs, dapp)
	if err != nil {
		log.Fatalf("Failed to parse App from %s: %s", appLocation, err)
	}

	objects, err := app.GenerateResources(&opts.SpaceSpec, dapp)
	if err != nil {
		log.Fatalf("Failed to generate resources for App: %s", err)
	}

	serializer := json.NewSerializerWithOptions(json.DefaultMetaFactory, nil, nil, json.SerializerOptions{Yaml: true, Pretty: true, Strict: true})
	for _, obj := range objects {
		err := serializer.Encode(obj, os.Stdout)
		if err != nil {
			panic(err)
		}
		fmt.Println("---")
	}
}

func printHelp() {
	fmt.Printf("usage: %s <path/to/app.yaml>\n\n", path.Clean(os.Args[0]))
	fmt.Printf("Generate Kubernetes resources from an App resource.\n\n")
	flag.PrintDefaults()
}
