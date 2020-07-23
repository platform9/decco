# -*- mode: Python -*-

docker_build('platform9/decco-operator', '.', dockerfile='Dockerfile')
# k8s_yaml(kustomize('./manifests_v2/ingress'))
# k8s_yaml(kustomize('./manifests_v2/k8sniff'))
k8s_yaml(kustomize('./manifests_v2'))
# k8s_resource('example-go', port_forwards=8000)