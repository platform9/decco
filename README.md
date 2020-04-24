# decco
Deployment cluster configuration and operations for Kubernetes

## Overview

Decco is a lightweight framework that simplifies the deployment,
 network configuration, and security hardening of Internet-facing
 applications in a highly multi-tenant environment.
 
The documentation is under construction, but in the meantime,
this Kubecon Austin 2017 presentation gives a good overview and
demonstration:

https://www.youtube.com/watch?v=tFdcrncaxD4&list=PLj6h78yzYM2P-3-xqvmWaZbbI1sW-ulZb&index=32

Decco solves and automates the following problems:
- Confining applications to well-defined boundaries which could be based on customer or geographical region
- Exposing applications on the Internet via automatic DNS and LoadBalancer configuration
- Securing communications using end-to-end TLS and Kubernetes Network Policies.
- Routing network requests to the correct application endpoints
- Collecting and aggregating log files



## Developing
Littering print statements throughout your code in order to debug complicated reconcilation loops can become difficult to reason about. With that said, this repo offers at least 1 opinionated way of setting break points for iterative development.

1. Create a file named `kubeconfig` at the root of this repo (same place as the Makefile)
2. Have [vscode](https://code.visualstudio.com/) installed
3. Go to the controller you would like to debug and set a breakpoint  
Ex: `pkg/controllers/app_controller.go` --> click to the left of a line number to place a red dot
4. Run `make operator-debug` to build the operator binary  
**NOTE:** if you get errors relating to controller-gen, try running `hack/setup_kubebuilder.sh`

5. Click on vscode's Debug tab and click the green button near the top-left  
This repo includes a launch.json configured to work with the above steps. 

### Go toolchain configuration

By default, kplane downloads a complete Go toolchain to `./build` and uses 
this within the Makefile. To ensure that you and the IDE are using, you will 
need to set the following manually:

```bash
export GOROOT=$(pwd)/build/go
export PATH=${GOROOT}/bin:$(PATH)
```

If you are using an IDE, also update the Go toolchain there too. In Goland, 
update the GOROOT in the project settings (`Go > GOROOT`).

Alternatively, you can also disable the use of the Go toolchain in ./build; 
just export an empty GO_TOOLCHAIN:

```bash
export GO_TOOLCHAIN=""
```