#!/bin/bash -e
export STUNNEL_DEBUG="${STUNNEL_DEBUG:-7}"
export STUNNEL_CLIENT_MODE="${STUNNEL_CLIENT_MODE:-no}"
export STUNNEL_CERT_FILE="${STUNNEL_CERT_FILE:-/etc/stunnel/certs/cert.pem}"
export STUNNEL_KEY_FILE="${STUNNEL_KEY_FILE:-/etc/stunnel/certs/key.pem}"
export STUNNEL_CA_FILE="${STUNNEL_CA_FILE:-/etc/stunnel/certs/ca.pem}"
export STUNNEL_VERIFY_CHAIN="${STUNNEL_VERIFY_CHAIN:-no}"
export STUNNEL_CONF="${STUNNEL_CONF:-/etc/stunnel/stunnel.conf}"
export STUNNEL_TEMPLATE="${STUNNEL_TEMPLATE:-/etc/stunnel/stunnel.conf.template}"

if [ ! -f "${STUNNEL_CERT_FILE}" ]; then
	echo ${STUNNEL_CERT_FILE} does not exist
	exit 1
fi

if [ ! -f "${STUNNEL_KEY_FILE}" ]; then
	echo ${STUNNEL_KEY_FILE} does not exist
	exit 1
fi

if [ -z "${STUNNEL_CONNECT}" ]; then
	echo STUNNEL_CONNECT not defined
	exit 1
fi

if [ -z "${STUNNEL_ACCEPT_PORT}" ]; then
	echo STUNNEL_ACCEPT_PORT not defined
	exit 1
fi

if [ "${STUNNEL_VERIFY_CHAIN}" == "yes" ]; then
	if [ ! -f "${STUNNEL_CA_FILE}" ]; then
		echo ${STUNNEL_CA_FILE} does not exist
		exit 1
	fi
	export STUNNEL_CAFILE_LINE="CAfile=${STUNNEL_CA_FILE}"
fi

cat ${STUNNEL_TEMPLATE} | envsubst > ${STUNNEL_CONF}
echo stunnel.conf contents:
echo --- begin ---
cat ${STUNNEL_CONF}
echo --- end ---
echo environment vars:
echo --- begin ---
env
echo --- end ---
exec stunnel
