#!/usr/bin/env python3
#
# Copyright 2019 Davi Gupta
#

from __future__ import print_function
from os import path
import argparse
import yaml
from kubernetes import client
from kubernetes.client.rest import ApiException
import re
from pprint import pprint

IMAGE = "davigupta/minion:0.14"

def main(pod, namespace, delete, host, ca, token, cluster):
    cfg = client.Configuration()
    cfg.host = "https://" + host
    cfg.ssl_ca_cert = ca
    cfg.assert_hostname = False
    cfg.api_key['authorization'] = token
    cfg.api_key_prefix['authorization'] = 'Bearer'

    with open(path.join(path.dirname(__file__), "deploy-t.yaml")) as f:
        norm_pod = re.sub("\.", "-", pod)
        if not delete:
            dep = yaml.safe_load(f)
            dep['metadata']['name'] = norm_pod
            dep['metadata']['namespace'] = namespace
            dep['spec']['template']['metadata']['labels']['app'] = norm_pod
            dep['spec']['template']['metadata']['labels']['cluster'] = cluster
            dep['spec']['template']['spec']['containers'][0]['image'] = IMAGE
            #print(dep)
        k8s_beta = client.ExtensionsV1beta1Api(client.ApiClient(cfg))
        try:
            if not delete:
                resp = k8s_beta.create_namespaced_deployment(body=dep, namespace=namespace)
            else:
                delete_options = client.V1DeleteOptions()
                delete_options.grace_period_seconds = 0
                delete_options.propagation_policy = 'Foreground'
                resp = k8s_beta.delete_namespaced_deployment(norm_pod, body=delete_options, namespace=namespace)
            pprint(resp)
        except ApiException as e:
            if not delete:
                print("Exception when calling ExtensionsV1beta1Api->create_namespaced_deployment: %s\n" % e)
            else:
                print("Exception when calling ExtensionsV1beta1Api->delete_namespaced_deployment: %s\n" % e)

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description="yaml generation for deployment")
    parser.add_argument('--pod', type=str, default="tom.com", help='pod name')
    parser.add_argument('--ns', type=str, default="default", help='namespace')
    parser.add_argument('--delete', action='store_true', help='delete namespace')
    parser.add_argument('--host', type=str, default="gateway.sjc.nextensio.net:6443", help='k8s api server')
    parser.add_argument('--ca', type=str, default="./api_ca.crt", help='certificate for verify')
    parser.add_argument('--token', type=str, default="", help='token for authorization')
    parser.add_argument('--cluster', type=str, default="1", help='cluster id')
    args = parser.parse_args()
    main(args.pod, args.ns, args.delete, args.host, args.ca, args.token, args.cluster)
