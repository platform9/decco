#!/bin/bash

set -e
# set -xe

SRC_ROOT=$(readlink -f $(dirname $0))
BUILD_DIR="$SRC_ROOT/build"
GCLOUD_DIR="$SRC_ROOT/build/gcloud"
GCLOUD_CONFIG_DIR="${GCLOUD_DIR}/config"
GCLOUD_SVC_KEY_FILE="${GCLOUD_DIR}/key.json"

GCLOUD_URL=${GCLOUD_URL:-"https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-sdk-176.0.0-linux-x86_64.tar.gz"}
GCLOUD_PROJECT=${GCLOUD_PROJECT:-"leb-project-2"}
GCLOUD_GKE_ZONE=${GCLOUD_GKE_ZONE:-"us-central1-b"}
GCLOUD_GKE_CLUSTER=${GCLOUD_GKE_CLUSTER:-"rbac-1"}

if [ -z "${GCLOUD_SVC_KEY_BASE64_CONTENT}" ] ; then
    echo "GCLOUD_SVC_KEY_BASE64_CONTENT undefined"
    exit 1
fi

mkdir -p ${GCLOUD_CONFIG_DIR}
echo $GCLOUD_SVC_KEY_BASE64_CONTENT |base64 -d > ${GCLOUD_SVC_KEY_FILE}
pushd ${GCLOUD_DIR}
wget ${GCLOUD_URL}
tar xf google-cloud-sdk-*.tar.gz
export PATH=${GCLOUD_DIR}/google-cloud-sdk/bin:$PATH
export CLOUDSDK_CONFIG=${GCLOUD_CONFIG_DIR}
export KUBECONFIG=${GCLOUD_CONFIG_DIR}/kubeconfig

gcloud auth activate-service-account --project ${GCLOUD_PROJECT} --key-file ${GCLOUD_SVC_KEY_FILE}
gcloud components install kubectl --quiet
gcloud container clusters get-credentials -z ${GCLOUD_GKE_ZONE} ${GCLOUD_GKE_CLUSTER}

which kubectl
kubectl get nodes
kubectl version
echo "${KUBECONFIG}"
