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