# Copyright (c) Platform9 systems. All rights reserved

# pylint: disable=dangerous-default-value,unused-variable,too-many-locals
# pylint: disable=too-many-arguments

import time
import logging
import base64
from kubernetes import client, config
from kubernetes.client.models.v1_secret import V1Secret
from kubernetes.client.models.v1_delete_options import V1DeleteOptions
# from kubernetes.client.models.extensions_v1beta1_deployment import ExtensionsV1beta1Deployment
# from kubernetes.client.models.extensions_v1beta1_deployment_spec import ExtensionsV1beta1DeploymentSpec
# from kubernetes.client.models.v1_object_meta import V1ObjectMeta
# from kubernetes.client.models.v1_pod_template_spec import V1PodTemplateSpec
# from kubernetes.client.models.v1_container import V1Container
# from kubernetes.client.models.v1_env_var import V1EnvVar
# from kubernetes.client.models.v1_pod_spec import V1PodSpec
from firkinize.configstore.consul import Consul
from tempfile import mkdtemp
from os import path
from decco_tests.utils.decco_api import DeccoApi
from setupd.config import Configuration, CertificateData
from setupd.fts import (create_and_verify_db_connection,
                        ensure_metadata_schema,
                        sync_to_consul)
from setupd.ecr import get_ecr_login_info
from string import Template
from contextlib import contextmanager

LOG = logging.getLogger(__name__)

import pf9lab.hosts.authorize as labrole
from pf9lab.retry import retry
from pf9lab.testbeds.common import generate_short_du_name
from pf9lab.du.auth import login
from pf9deploy.server.util.passwords import generate_random_password
from pf9deploy.server.secrets import SecretsManager
from pf9lab.testbeds import Testbed
# from qbert_tests.testbeds import aws_utils as qbaws
import re
import os
from subprocess import Popen, PIPE


# CSPI_MISC_DIR = pjoin(dirname(decco_tests.__file__), 'misc')
CSPI_MISC_DIR = ''
AWS_REGION = os.getenv('AWS_REGION', 'us-west-1')
CONTAINER_IMAGES_FILE = os.getenv('CONTAINER_IMAGES_FILE')
config.load_kube_config()

stunnel_path = os.getenv('STUNNEL_PATH')
if not stunnel_path:
    raise Exception('STUNNEL_PATH not defined')


@retry(log=LOG, max_wait=60, interval=10)
def retried_login(*largs, **kwargs):
    return login(*largs, **kwargs)


def new_configuration(db, admin_user, shortname, state_fqdn, region):
    cfg = Configuration()
    cfg.customer.fullname = 'decco test customer'
    cfg.customer.admin_user = admin_user
    cfg.customer.shortname = shortname
    cfg.fqdn = state_fqdn
    cfg.region = region
    cfg.release_version = 'platform9-decco-1.0.0'
    cfg.save(db)
    cfg.sync_certificates()
    cfg.sync_passwords()
    cfg.save(db)
    return cfg


def checked_local_call(cmd):
    p = Popen(cmd, stdout=PIPE)
    p.wait()
    if p.returncode != 0:
        raise Exception('command %s returned %d' % (' '.join(cmd), p.returncode))
    return p.stdout.read()

def generate_setupd_valid_password():
    """
    setupd requires that passwords contain at least one digit, one uppercase
    letter and one lowercase letter. pf9deploy's generate_random_password
    can sometimes violate this, so check it before using it.
    If we can't do it in less than 100 iterations, something is really wrong,
    so fail.
    FIXME: This code is pretty much copied from pf9_setup.py. We should pull
    it into a third place where it can be used by both - maybe in firkinize.
    """
    validation_regexes = [
        re.compile(r'[0-9]'),
        re.compile(r'[a-z]'),
        re.compile(r'[A-Z]')
    ]
    def _valid_password(passwd):
        if len(passwd) < 10:
            return False

        for pwd_rgx in validation_regexes:
            if not pwd_rgx.search(passwd):
                return False
        return True

    tries = 0
    while tries < 100:
        tries += 1
        passwd = generate_random_password()
        if _valid_password(passwd):
            LOG.info('Generated good password in %d attempt(s)', tries)
            return passwd
    raise RuntimeError('Failed to generate setupd acceptable password!')


def create_dockercfg_secret(namespace, secret_name, server, user, password):
    kubectl_path = os.getenv('KUBECTL_PATH')
    if not kubectl_path:
        raise Exception('KUBECTL_PATH not defined')
    checked_local_call([
        kubectl_path,
        'create',
        '--namespace=%s' % namespace,
        'secret',
        'docker-registry',
        secret_name,
        '--docker-email=none',
        '--docker-server=%s' % server,
        '--docker-username=%s' % user,
        '--docker-password=%s' % password
    ])


def setup_decco_hosts(du_address, hosts, admin_user, admin_password, token):
    """
    Install hostagent on all the hosts, then enable and wait for the qbert
    role. Adds the resmgr host id to the each host's dictionary if hostagent
    is installer successfully.
    """
    if not hosts:
        LOG.info('No kube hosts to setup')
        return

    for host in hosts:
        labrole.install_certless_hostagent(du_address,
                                           host['ip'],
                                           admin_user,
                                           admin_password,
                                           'service')
    for host in hosts:
        host_info = labrole.wait_unauthed_role(du_address,
                                               token,
                                               host['hostname'],
                                               'pf9-kube')
        host['host_id'] = host_info['id']
        labrole.authorize_role(du_address, host['host_id'], 'pf9-kube', token)

    for host in hosts:
        labrole.wait_for_role(du_address, host['host_id'], 'pf9-kube', token)


def start_mysql(namespace):
    root_passwd = generate_setupd_valid_password()
    spec = {
        'initialReplicas': 1,
        'verifyTcpClientCert': True,
        'pod': {
            'containers': [
                {
                    'name': 'mysql',
                    'image': 'mysql',
                    'env': [
                        {
                            'name': 'MYSQL_ROOT_PASSWORD',
                            'value': root_passwd
                        }
                    ],
                    'ports': [
                        {
                            'containerPort': 3306,
                        }
                    ]
                }
            ]
        }
    }

    dapi = DeccoApi()
    for i in range(5):
        try:
            time.sleep(2)
            dapi.create_app('mysql', spec, namespace)
            LOG.info('successfully created mysql app')
            return root_passwd
        except:
            LOG.info("failed to create mysql app, may retry...")
    raise Exception('failed to create mysql app')


def start_keystone(namespace, keystone_image_uri, customer_uuid, region_uuid,
                   image_pull_secret_name):
    config_url = "http://consul-cleartext.global.svc.cluster.local:8500"
    config_host_and_port = "consul-cleartext.global.svc.cluster.local:8500"
    spec = {
        'initialReplicas': 1,
        'httpUrlPath': '/keystone',
        'pod': {
            'initContainers': [
                {
                    'name': 'init-region',
                    'image': keystone_image_uri,
                    'command': [
                        "/root/init-region",
                        "--config-url",
                        config_url,
                        "--customer-id",
                        customer_uuid,
                        "--region-id",
                        region_uuid
                    ]
                }
            ],
            'containers': [
                {
                    'name': 'keystone',
                    'image': keystone_image_uri,
                    'env': [
                        {
                            'name': 'CONFIG_URL',
                            'value': config_url
                        },
                        {
                            'name': 'CONFIG_HOST_AND_PORT',
                            'value': config_host_and_port
                        },
                        {
                            'name': 'CUSTOMER_ID',
                            'value': customer_uuid
                        },
                        {
                            'name': 'REGION_ID',
                            'value': region_uuid
                        },
                    ],
                    'ports': [
                        {
                            'containerPort': 5000,
                        }
                    ]
                }
            ],
            'imagePullSecrets': [
                {
                    'name': image_pull_secret_name
                }
            ]
        }
    }

    dapi = DeccoApi()
    for i in range(5):
        try:
            time.sleep(2)
            dapi.create_app('keystone', spec, namespace)
            LOG.info('successfully created keystone app')
            return
        except:
            LOG.info("failed to create keystone app, may retry...")
    raise Exception('failed to create mysql app')


def create_http_wildcard_cert_secret(secret_name, domain):
    sm = SecretsManager()
    cert_entry = sm.db.certs.find_one({'type': 'wildcard', 'domain': domain})
    certdata = sm.get_secret(cert_entry['tags']['cert'])
    certdata = base64.b64encode(certdata)
    keydata = sm.get_secret(cert_entry['tags']['key'])
    keydata = base64.b64encode(keydata)
    v1 = client.CoreV1Api()
    secret = V1Secret(metadata={'name': secret_name})
    secret.data = {
        'tls.crt': certdata,
        'tls.key': keydata
    }
    v1.create_namespaced_secret('decco', secret)


def create_tcp_wildcard_ca_and_cert(customer_shortname, customer_fqdn):
    ca = CertificateData.generate_ca(cn=customer_shortname,
                                     du_id=0,
                                     set_version=0)
    tcp_wildcard_cn = '*.%s' % customer_fqdn
    tcp_cert = CertificateData.generate_certificate(tcp_wildcard_cn, ca)
    return ca, tcp_cert


def create_tcp_wildcard_cert_secret(secret_name, ca, tcp_cert):
    ca_cert_base64 = base64.b64encode(ca.cert_pem)
    tcp_cert_base64 = base64.b64encode(tcp_cert.cert_pem)
    tcp_key_base64 = base64.b64encode(tcp_cert.private_key_pem)
    secret = V1Secret(metadata={'name': secret_name})
    secret.data = {
        'ca.pem': ca_cert_base64,
        'key.pem': tcp_key_base64,
        'cert.pem': tcp_cert_base64
    }
    v1 = client.CoreV1Api()
    v1.create_namespaced_secret('decco', secret)


def read_consul_certs():
    v1 = client.CoreV1Api()
    s = v1.read_namespaced_secret('tcp-cert-global', 'global')
    client_key = base64.b64decode(s.data['key.pem'])
    client_cert = base64.b64decode(s.data['cert.pem'])
    ca_cert = base64.b64decode(s.data['ca.pem'])
    return ca_cert, client_cert, client_key


def generate_stunnel_config(fqdn, svc_name, svc_port,
                            ca_cert, client_cert, client_key):
    tmp_dir = mkdtemp(fqdn)
    with open(path.join(tmp_dir, 'ca.pem'), 'w') as f:
        f.write(ca_cert)
    with open(path.join(tmp_dir, 'cert.pem'), 'w') as f:
        f.write(client_cert)
    with open(path.join(tmp_dir, 'key.pem'), 'w') as f:
        f.write(client_key)
    template = """
socket=l:TCP_NODELAY=1
socket=r:TCP_NODELAY=1

debug=7
# output=/dev/stdout
foreground=yes

[app]
client=yes
accept=${svc_port}
connect=${fqdn}:443
sni=${svc_name}.${fqdn}
# checkHost = ${svc_name}.${fqdn}
cert=${tmp_dir}/cert.pem
key=${tmp_dir}/key.pem
verifyChain=yes
CAfile=${tmp_dir}/ca.pem
"""
    tmpl = Template(template=template)
    conf = tmpl.substitute({}, fqdn=fqdn, svc_name=svc_name,
                           svc_port=svc_port, tmp_dir=tmp_dir)
    stunnel_conf_path = path.join(tmp_dir, 'stunnel.conf')
    with open(stunnel_conf_path, 'w') as f:
        f.write(conf)
    return tmp_dir, stunnel_conf_path

@contextmanager
def stunnel(stunnel_conf_path, output_dir):
    output_path = path.join(output_dir, 'stunnel.log')
    with open(output_path, 'w') as f:
        popen = Popen([stunnel_path, stunnel_conf_path], stdout=f, stderr=f)
        LOG.info('popen process pid: %s and log: %s', popen.pid, output_path)
        try:
            time.sleep(2) # for race condition connecting to the local port
            yield popen
        finally:
            popen.terminate()


def _get_db_connection(mysql_root_passwd):
    for attempt in range(5):
        try:
            time.sleep(10)
            db = create_and_verify_db_connection('127.0.0.1', 3306,
                                                 'root',
                                                 mysql_root_passwd)
            return db
        except Exception as ex:
            LOG.info('failed to connect to db, may retry in a bit (%s)', ex)
    raise Exception('failed to connect to db')

class DeccoTestbed(Testbed):
    """
    testbed with no DU, rather 1 host that sort of acts like one.
    Has rabbitmq and consul (via container) installed.
    """

    def __init__(self, tag, kube_config_base64, global_space_info):
        # self.hosts = []
        super(DeccoTestbed, self).__init__()
        self.kube_config_base64 = kube_config_base64
        self.tag = tag
        self.global_space_info = global_space_info


    @classmethod
    def create(cls, tag):

        keystone_image = os.getenv('KEYSTONE_CONTAINER_IMG')
        keystone_tag = os.getenv('KEYSTONE_CONTAINER_TAG')
        if not keystone_image or not keystone_image:
            raise Exception('KEYSTONE_CONTAINER_IMG and KEYSTONE_CONTAINER_TAG must be defined')
        keystone_image_uri = '%s:%s' % (keystone_image, keystone_tag)
        # Note that the only compatible (image, flavor) combinations are
        # centos7-latest, ubuntu16 and ubuntu16, with 1cpu.2gb.40gb, at least
        # that I know of as of 9/19/17 -Bob
        kubeConfigPath = os.getenv('KUBECONFIG')
        if kubeConfigPath is None:
            raise Exception('KUBECONFIG not defined')
        with open(kubeConfigPath, "r") as file:
            data = file.read()
            kube_config_base64 = base64.b64encode(data)

        registry_url = os.getenv('REGISTRY_URL')
        if not registry_url:
            raise Exception('Where are we pulling containers from?')

        image_tag = os.getenv('IMAGE_TAG', 'latest')

        aws_access_key = os.getenv('AWS_ACCESS_KEY')
        aws_secret_key = os.getenv('AWS_SECRET_KEY')
        if not aws_access_key or not aws_secret_key:
            raise Exception('AWS credentials are required to pull from ECR')

        docker_user, docker_token = get_ecr_login_info(aws_access_key,
                                                   aws_secret_key,
                                                   registry_url)

        consul_ca_cert, consul_client_cert, consul_client_key = \
            read_consul_certs()


        # install container image/tag list
        #if CONTAINER_IMAGES_FILE:
        #    if not os.path.isfile(CONTAINER_IMAGES_FILE):
        #        LOG.warning('images file set to %s but does not exist?',
        #                CONTAINER_IMAGES_FILE)
        #    else:
        #        with open(CONTAINER_IMAGES_FILE, 'r') as f:
        #            LOG.info(yaml.load(f.read()))
        #        with typical_fabric_settings(controller['ip']):
        #            put(CONTAINER_IMAGES_FILE, '/etc/setupd.images.in')

        LOG.info('image tag: %s', image_tag)

        customer_shortname = os.getenv('CUSTOMER_SHORTNAME')
        if not customer_shortname:
            customer_shortname = generate_short_du_name(tag)

        admin_user = 'whoever@example.com'
        admin_password = generate_setupd_valid_password()

        domain = 'platform9.horse'
        customer_fqdn = '%s.%s' % (customer_shortname, domain)
        region_name = 'RegionOne'
        region_fqdn = '%s-%s.%s' % (customer_shortname, region_name, domain)

        ca, tcp_cert = create_tcp_wildcard_ca_and_cert(customer_shortname,
                                                       customer_fqdn)
        tcp_cert_secret_name = 'tcp-cert-%s' % customer_shortname
        create_tcp_wildcard_cert_secret(tcp_cert_secret_name, ca, tcp_cert)

        http_cert_secret_name = 'http-cert-%s' % customer_shortname
        create_http_wildcard_cert_secret(http_cert_secret_name, domain)
        dapi = DeccoApi()
        global_space_spec = {
            'domainName': domain,
            'httpCertSecretName': http_cert_secret_name,
            'tcpCertAndCaSecretName': tcp_cert_secret_name
        }
        dapi.create_space(customer_shortname, global_space_spec)
        mysql_root_passwd = start_mysql(customer_shortname)

        tmp_dir, stunnel_conf_path = generate_stunnel_config(
            customer_fqdn, 'mysql', 3306,
            ca.cert_pem, tcp_cert.cert_pem, tcp_cert.private_key_pem)
        LOG.info('stunnel conf path: %s' % stunnel_conf_path)

        state_data = {
            'consul_url': 'http://localhost:8500',
            # 'consul_url': 'http://consul.global.svc.cluster.local:8500',
            'admin_user': admin_user,
            'admin_password': admin_password,
            'db_host': 'mysql-cleartext',
            'db_user': 'root',
            'db_password': mysql_root_passwd
        }

        with stunnel(stunnel_conf_path, tmp_dir):
            LOG.info('stunnel started')
            db = _get_db_connection(mysql_root_passwd)
            with db.cursor() as cursor:
                cursor.execute('CREATE DATABASE IF NOT EXISTS `pf9_metadata`')
            db.commit()
            db.select_db('pf9_metadata')
            ensure_metadata_schema(db)
            cfg = new_configuration(db, admin_user, customer_shortname,
                                    customer_fqdn, region_name)

            LOG.info('db setup complete, preparing to sync to consul')

            tmp_dir, stunnel_conf_path = generate_stunnel_config(
                'global.platform9.horse', 'consul', 8500, consul_ca_cert,
                consul_client_cert, consul_client_key)

            with stunnel(stunnel_conf_path, tmp_dir):
                LOG.info('stunnel to consul running')
                _, dbserver_id = sync_to_consul(cfg, state_data)

        image_pull_secret = 'regsecret'
        create_dockercfg_secret(customer_shortname, image_pull_secret,
                                registry_url, docker_user, docker_token)

        start_keystone(customer_shortname, keystone_image_uri,
                       cfg.customer.uuid, cfg.uuid, image_pull_secret)

        # LOG.info('Adding %s to route53 for %s...',
        #          customer_fqdn, controller['ip'])
        # qbaws.create_dns_record([controller['ip']], customer_fqdn)

        # webcert, webkey = put_wildcard_keypair(controller['ip'], domain)


        LOG.info('waiting for keystone to become open')
        LOG.info('obtaining token')
        # user-watch might need a few seconds to propagate the initial admin user
        #token = 'dummy_token'
        token = None
        if not token:
            token_info = retried_login('https://%s' % customer_fqdn,
                                       'whoever@example.com', admin_password,
                                       'service')
            token = token_info['access']['token']['id']
            tenant_id = token_info['access']['token']['tenant']['id']
            LOG.info('token: %s', str(token_info))

        global_space_info = {
            'name': customer_shortname,
            'mysql_root_passwd': mysql_root_passwd,
            'customer_uuid': cfg.customer.uuid,
            'region_uuid': cfg.uuid,
            'dbserver_id': dbserver_id,
            'keystone_token': token,
            'spec': global_space_spec
        }

        return cls(tag, kube_config_base64, global_space_info)

    @staticmethod
    def from_dict(desc):
        """ desc is a dict """
        type_name = '.'.join([__name__, DeccoTestbed.__name__])
        if desc['type'] != type_name:
            raise ValueError('attempt to build %s with %s' %
                             (type_name, desc['type']))
        return DeccoTestbed(desc['tag'],
                            desc['kube_config_base64'],
                            desc['global_space_info']
                            )

    def to_dict(self):
        return {
            'type': '.'.join([__name__, DeccoTestbed.__name__]),
            'kube_config_base64': self.kube_config_base64,
            'global_space_info': self.global_space_info,
            'tag': self.tag
        }

    def destroy(self):
        LOG.info('Destroying decco testbed')
        dapi = DeccoApi()
        try:
            dapi.delete_space(self.global_space_info['name'])
        except:
            LOG.exception("warning: failed to delete global space")
        global_space_spec = self.global_space_info['spec']
        v1 = client.CoreV1Api()
        for key in ['httpCertSecretName', 'tcpCertAndCaSecretName']:
            try:
                secret_name = global_space_spec[key]
                v1.delete_namespaced_secret(secret_name, 'decco',
                                            V1DeleteOptions())
            except:
                LOG.exception("warning: failed to delete secret")
        try:
            consul_ca_cert, consul_client_cert, consul_client_key = \
                read_consul_certs()
            tmp_dir, stunnel_conf_path = generate_stunnel_config(
                'global.platform9.horse', 'consul', 8500, consul_ca_cert,
                consul_client_cert, consul_client_key)

            with stunnel(stunnel_conf_path, tmp_dir):
                LOG.info('stunnel to consul running')
                consul = Consul('http://localhost:8500')
                prefix = 'dbservers/%s' % self.global_space_info['dbserver_id']
                consul.kv_delete_prefix(prefix)
                prefix = 'customers/%s' % self.global_space_info['customer_uuid']
                consul.kv_delete_prefix(prefix)

        except Exception as exc:
            LOG.warn("warning: failed to delete consul keys: %s", exc)

