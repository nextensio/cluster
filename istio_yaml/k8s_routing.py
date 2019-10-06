#!/usr/bin/env python3
#
# Copyright 2019 Davi Gupta
#

from __future__ import print_function
from os import path
import argparse
from pprint import pprint
import yaml
import re
import subprocess

def main(service, pod, namespace, delete, gateway, kube):
    norm_pod = re.sub("\.", "-", pod)
    ylist = []
    with open(path.join(path.dirname(__file__), "routing-t.yaml")) as f:
        dep = yaml.safe_load_all(f)
        i = 0
        for ele in dep:
            ele['metadata']['namespace'] = namespace
            if not i:
                ele['metadata']['name'] = norm_pod + "-vs"
                ele['spec']['hosts'] = gateway
                ele['spec']['http'][0]['match'][0]['headers']['x-nextensio-for']['prefix'] = service
                ele['spec']['http'][0]['route'][0]['destination']['host'] = norm_pod + "-in"
                ele['spec']['http'][1]['match'][0]['headers']['x-nextensio-connect']['prefix'] = service
                ele['spec']['http'][1]['route'][0]['destination']['host'] = norm_pod + "-out"
            else:
                if i == 1:
                    ele['metadata']['name'] = norm_pod + "-ingress"
                    ele['spec']['host'] = norm_pod + "-in"
                else:
                    ele['metadata']['name'] = norm_pod + "-egress"
                    ele['spec']['host'] = norm_pod + "-out"
                ele['spec']['subsets'][0]['name'] = norm_pod
            #print(ele)
            ylist.append(ele)
            i = i + 1
    f.close()

    with open(path.join(path.dirname(__file__), norm_pod + "-routing.yaml"), "w") as f:
        yaml.dump_all(ylist, f, default_flow_style=False)
    f.close()
    if not delete:
        subprocess.run(["./k8s_apply.sh", kube, norm_pod + "-routing.yaml"])
    else:
        subprocess.run(["./k8s_delete.sh", kube, norm_pod + "-routing.yaml"])

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description="yaml generation for routing")
    parser.add_argument('--service', type=str, default="tom.com", help='service to route')
    parser.add_argument('--ns', type=str, default="default", help='namespace')
    parser.add_argument('--delete', action='store_true', help='delete namespace')
    parser.add_argument('--gateway', type=str, default="gateway.sjc.nextensio.net", help='istio gateway')
    parser.add_argument('--pod', type=str, default="tom", help='destination pod for service')
    parser.add_argument('--kube', type=str, default="${HOME}/.kube/config", help='kube config file')
    args = parser.parse_args()
    main(args.service, args.pod, args.ns, args.delete, args.gateway, args.kube)
