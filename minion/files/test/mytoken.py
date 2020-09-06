#!/usr/bin/env python3
#
# Author: Davi Gupta (davigupta@gmail.com), Mar 2019
# TOKEN=$(curl https://raw.githubusercontent.com/istio/istio/release-1.0/security/tools/jwt/samples/demo.jwt -s)
#

import http.client
HOST = "raw.githubusercontent.com"
PATH = "/istio/istio/release-1.0/security/tools/jwt/samples/demo.jwt"

def get_token():
    token = 'error'
    connection = http.client.HTTPSConnection(HOST)
    connection.request("GET", PATH)
    response = connection.getresponse()
    print("Status: {} and reason: {}".format(response.status, response.reason))
    if response.status == 200:
        str = response.read()
        token = str.decode("utf-8").rstrip('\n')
    connection.close()
    return token

if __name__ == '__main__':
    token = get_token()
    print(token)
