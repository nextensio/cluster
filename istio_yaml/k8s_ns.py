#!/usr/bin/env python3
#
# Copyright 2019 Davi Gupta
#

from __future__ import print_function
import argparse
from kubernetes import client
from kubernetes.client.rest import ApiException
from pprint import pprint

def main(namespace, delete, host, ca, token):
    cfg = client.Configuration()
    cfg.host = "https://" + host
    #cfg.verify_ssl = False
    cfg.ssl_ca_cert = ca
    cfg.assert_hostname = False
    cfg.api_key['authorization'] = token
    cfg.api_key_prefix['authorization'] = 'Bearer'

    k8s_api = client.CoreV1Api(client.ApiClient(cfg))
    if not delete:
        body = client.V1Namespace()
        body.metadata = client.V1ObjectMeta(name=namespace, labels={"istio-injection" : "enabled"})
        include_uninitialized = False

    try:
        if not delete:
            api_res = k8s_api.create_namespace(body, include_uninitialized=include_uninitialized)
        else:
            api_res = k8s_api.delete_namespace(namespace)
        pprint(api_res)
    except ApiException as e:
        if not delete:
            print("Exception when calling CoreV1Api->create_namespace: %s\n" % e)
        else:
            print("Exception when calling CoreV1Api->delete_namespace: %s\n" % e)
 
if __name__ == '__main__':
    parser = argparse.ArgumentParser(description="create namespace")
    parser.add_argument('--ns', type=str, default="honey", help='namespace')
    parser.add_argument('--delete', action='store_true', help='delete namespace')
    parser.add_argument('--host', type=str, default="gateway.sjc.nextensio.net:6443", help='k8s api server')
    parser.add_argument('--ca', type=str, default="./api_ca.crt", help='certificate for verify')
    parser.add_argument('--token', type=str, default="", help='token for authorization')
    args = parser.parse_args()
    main(args.ns, args.delete, args.host, args.ca, args.token)
