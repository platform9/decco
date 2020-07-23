# Developing

## 1. Setup a Kubernetes Cluster

To develop Decco, you need a Kubernetes with LoadBalancer support. One option is
to use KinD + MetalLB.

```bash
kind create cluster --config ./kind-dev.yaml
```


```bash
kubectl apply -f https://raw.githubusercontent.com/metallb/metallb/v0.9.3/manifests/namespace.yaml
kubectl apply -f https://raw.githubusercontent.com/metallb/metallb/v0.9.3/manifests/metallb.yaml

# On first install only
kubectl create secret generic -n metallb-system memberlist --from-literal=secretkey="$(openssl rand -base64 128)"
```

Accessing a Space's ingress
```
curl --insecure --header "Host: example-space-2.platform9.horse" https://localhost:30443 
```