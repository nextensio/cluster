#!/usr/bin/env sh

/go/bin/minion.io -tunnel -consul_dns -register -ip 127.0.0.1 -iport 80 &

tail -f /dev/null

