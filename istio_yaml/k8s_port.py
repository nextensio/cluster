#!/usr/bin/env python3
#
# Copyright 2019 Davi Gupta
#

from __future__ import print_function
from os import path
import argparse
from kubernetes import client
from kubernetes.client.rest import ApiException
from pprint import pprint
import yaml
import re

def main(pod, namespace, delete, host, ca, token):
    cfg = client.Configuration()
    cfg.host = "https://" + host
    cfg.ssl_ca_cert = ca
    cfg.assert_hostname = False
    cfg.api_key['authorization'] = token
    cfg.api_key_prefix['authorization'] = 'Bearer'

    k8s_api = client.CoreV1Api(client.ApiClient(cfg))
    with open(path.join(path.dirname(__file__), "port-t.yaml")) as f:
        norm_pod = re.sub("\.", "-", pod)
        dep = yaml.safe_load_all(f)

        i = 0
        for ele in dep:
            if not i:
                suffix = "-in"
                prefix = "http-"
            else:
                suffix = "-out"
                prefix = ""
            i = i + 1
            ele['metadata']['name'] = norm_pod + suffix
            ele['metadata']['namespace'] = namespace
            ele['metadata']['labels']['app'] = norm_pod
            ele['spec']['ports'][0]['name'] = prefix + norm_pod + suffix
            ele['spec']['selector']['app'] = norm_pod
            #print(ele)
            try:
                if not delete:
                    resp = k8s_api.create_namespaced_service(body=ele, namespace=namespace)
                else:
                    resp = k8s_api.delete_namespaced_service(norm_pod + suffix, namespace=namespace)
                pprint(resp)
            except ApiException as e:
                if not delete:
                    print("Exception when calling CoreV1Api->create_namespaced_service: %s\n" % e)
                else:
                    print("Exception when calling CoreV1Api->delete_namespaced_service: %s\n" % e)

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description="yaml generation for pod port")
    parser.add_argument('--pod', type=str, default="tom.com", help='pod name')
    parser.add_argument('--ns', type=str, default="default", help='namespace')
    parser.add_argument('--delete', action='store_true', help='delete namespace')
    parser.add_argument('--host', type=str, default="gateway.sjc.nextensio.net:6443", help='k8s api server')
    parser.add_argument('--ca', type=str, default="./api_ca.crt", help='certificate for verify')
    parser.add_argument('--token', type=str, default="", help='token for authorization')
    args = parser.parse_args()
    main(args.pod, args.ns, args.delete, args.host, args.ca, args.token)
