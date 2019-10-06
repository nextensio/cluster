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

def main(gateway, delete):
    with open(path.join(path.dirname(__file__), "gateway-t.yaml")) as f:
        dep = yaml.safe_load(f)
        dep['spec']['servers'][0]['hosts'] = gateway
        dep['spec']['servers'][1]['hosts'] = gateway
    f.close()

    with open(path.join(path.dirname(__file__), gateway + "-gateway.yaml"), "w") as f:
        yaml.dump(dep, f)
    f.close()
    #pprint(dep)
    if not delete:
        subprocess.run(["./k8s_apply.sh", gateway + "-gateway.yaml"])
    else:
        subprocess.run(["./k8s_delete.sh", gateway + "-gateway.yaml"])

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description="yaml generation for gateway")
    parser.add_argument('--gateway', type=str, default="gateway.sjc.nextensio.net", help='gateway dns name')
    parser.add_argument('--delete', action='store_true', help='delete gateway')
    args = parser.parse_args()
    main(args.gateway, args.delete)
