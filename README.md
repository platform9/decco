# decco v1
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


# deccov2
The overview above still applies to v2. The intent is to solve the same problems.

V2's focus is to move the codebase to a standardized kubebuilder-based codebase, as well as introduce Istio integration as the primary provider of mTLS and isolation of Spaces from other Spaces.

## Kubebuilder
[Quick-Start instructions (definitely install kustomize)](https://book.kubebuilder.io/quick-start.html)
[Install Delve with Go 1.12+](go get -u github.com/go-delve/delve/cmd/dlv)

Created multiple v1beta3 resources. Space, Manifest, App. [UPDATE: Manifest may not be needed]

When making a change to the CRD, in order to have it go into affect on the cluster you're debugging, run:
```
# Install is most important to get the updated CRDs into your cluster!
# The cluster you will deploy into with `make install` is whatever cluster is returned by `kubectl config current-context` for the kubeconfig located at `~/.kube/config`
$ make all && make install
```


# Improvements and how to do them with Istio

[Istio Gateway](https://istio.io/docs/reference/config/networking/v1alpha3/gateway/) can be used to replace nginx-ingress & k8s-sniff

[Istio mTLS](https://istio.io/docs/tasks/security/authn-policy/#globally-enabling-istio-mutual-tls) can be used to replace the requirement of Ingress/Egress declarations in Decco's App CRD.

[Istio Namespace-wide Policy](https://istio.io/docs/tasks/security/authn-policy/#namespace-wide-policy) Use this in-place of creating Ingress resources when creating a Space CRD.

Keep this part of an App's Spec `createDnsRecord: false` & maybe have a way to override what Istio VirtualService would be created by Decco by allowing the App to specify VirtualService.Spec.Http and VirtualService.Spec.Tcp. The rest of the VirtualService Spec will be handled by Decco based on the Space the App is being deployed into. [https://github.com/istio/api/blob/master/networking/v1alpha3/virtual_service.pb.go#L119:6](https://github.com/istio/api/blob/master/networking/v1alpha3/virtual_service.pb.go#L119:6) ← This actually cannot be used by us... and the knative team realized this themselves. So they maintain a k8s-usable version of it. [https://godoc.org/knative.dev/pkg/apis/istio/v1alpha3#VirtualServiceSpec](https://godoc.org/knative.dev/pkg/apis/istio/v1alpha3#VirtualServiceSpec)





# Debugging/development
I've uploaded my .vscode/launch.json file for VSCode users.
There is a flag which gets passed to the `manager` binary under `./bin/` which defines what kubeconfig to use. By default the kubeconfig under `~/.kube/config` is used, as well as the default context for that kubeconfig. You may change the value to point to a kubeconfig corresponding to the cluster you wish to test your changes against.`





