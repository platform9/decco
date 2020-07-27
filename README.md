# Decco

Deployment cluster configuration and operations for Kubernetes.

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
Littering print statements throughout your code in order to debug complicated 
reconcilation loops can become difficult to reason about. With that said, this 
repo offers at least 1 opinionated way of setting break points for iterative 
development.

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

## Release Workflow

Decco uses semantic versioning. To release a new version of Decco. 

1. (a) If you are releasing a **major** or **minor** version, such as v1.0.0 or 
   v1.3.0, you create a new branch, named `release-x.y`, where `x` and `y` are 
   the major and minor version respectively. Or,
   (b) if you are releasing a **patch**, such as 1.3.5, you check out the existing 
   branch with the appropriate major and minor version. For example, for 1.3.5
   check out `release-1.3`.

2. On the release branch, create and push a commit that bumps 
   [VERSION](./VERSION) to your desired version. Do not push this commit to the 
   master branch.

3. If needed, thoroughly test the release branch.

4. Then, run the (Platform9 internal) `decco-release` TeamCity build. This will
   effectively run the release script: [./hack/ci-release.sh](./hack/ci-release.sh).
   Alternatively, in a preconfigured environment you could run `make release` 
   instead.   

5. If everything succeeds, the version commit will be tagged, Github release 
   will be created and Docker images will be published for the given version.
   
In case you need to revert a release, you will need to manually delete the git 
tag, the github release, and delete the created Docker images. You will also 
need to re-tag the previous images to `latest`.   