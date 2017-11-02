#!/bin/bash

# env example:
# REGISTRY_URL=514845858982.dkr.ecr.us-west-1.amazonaws.com
# ONPREM_RPM_DIR=/home/loser/git/pf9-onprem/build/rpm/RPMS/x86_64/
# AWS_ACCESS_KEY=abcd
# AWS_SECRET_KEY=1234
# IMAGE_TAG=earliest (not required, defaults to latest)

set -xe

#if [ -z $KEYSTONE_CONTAINER_IMG ]; then
#    echo >&2 'please set KEYSTONE_CONTAINER_IMG to the repository/img location of pf9-keystone'
#    exit 1
#fi
#KEYSTONE_CONTAINER_TAG=${KEYSTONE_CONTAINER_TAG:-"latest"}

SRC_ROOT=$(readlink -f $(dirname $0))
PF9_MAIN=${PF9_MAIN:-"${SRC_ROOT}/../pf9-main"}

pushd ${PF9_MAIN}/testing
./setup_virtual_env_for_tests.sh
popd

PF9_VENV=${PF9_MAIN}/build/testing/venv
. ${PF9_VENV}/bin/activate
pushd ${SRC_ROOT}/tests
pip install -e .
popd
pip install kubernetes

#${PF9_VENV}/bin/pip install awscli

#if [ ! -d "${SRC_ROOT}/../firkinize" ]; then
#    echo >&2 "Firkinize not found. Exiting."
#    exit 1
#elif [ ! -d "${SRC_ROOT}/../pf9-qbert/tests" ]; then
#    echo >&2 "Please clone the pf9-qbert repo. Exiting."
#    exit 1
#fi

#pip install -e ${SRC_ROOT}/../firkinize -e ${SRC_ROOT}/../pf9-qbert/tests
#make -C ${SRC_ROOT}/../pf9-qbert kubernetes-test

TESTBED_RESULTS_DIR=${SRC_ROOT}/build/results
mkdir -p ${TESTBED_RESULTS_DIR}

#if [ -d $CONTAINER_IMGS_DIR ]; then
#    echo "Building images file from ${CONTAINER_IMGS_DIR}"
#    ${SRC_ROOT}/build_images_file.py ${CONTAINER_IMGS_DIR} >${SRC_ROOT}/setupd.images.in
#    export CONTAINER_IMAGES_FILE="${SRC_ROOT}/setupd.images.in"
#fi

export DU_ENV=neutron_dogfood
export VSPHERE_PASSWORD=whocares

delayed_tail() {
    while [ ! -f ${TESTBED_RESULTS_DIR}/*/stdout.txt ]; do
        sleep 3
    done
    tail -f ${TESTBED_RESULTS_DIR}/*/stdout.txt
}
# so you're not staring at an itestrun.py invocation for 5+ minutes
#delayed_tail &

#${PF9_VENV}/bin/python ${PF9_MAIN}/testing/itestrun.py \
#    -l DEBUG \
#    --resultdir ${SRC_ROOT}/build/results \
#    --suite-files ${SRC_ROOT}/tests/decco_tests/suites/decco.json \
#    ${ADDITIONAL_ITESTRUN_OPTS}

#kill %1
