#!/bin/bash

if [[ $# -ne 2 ]]; then
    echo "usage: $0 <config-file> <yaml-file>"
    exit 1
fi

./kubectl --kubeconfig=$1 delete -f $2
