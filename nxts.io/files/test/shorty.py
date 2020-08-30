#!/usr/bin/env python3
# 
# Davi Gupta, Mar 2019
#

import asyncio
#from websocket import create_connection
import websockets
import argparse
import logging
import re
import sys
import ssl
import mytoken
import pdata

port = 8002

headers = {'x-nextensio-codec': 'http',
           'x-nextensio-connect': 'abc.com',
           'x-nextensio-uuid': '12345678',
           "Authorization": "Bearer "}
url = ""
prefix = ""
ip_addr_regex = re.compile(r'\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b')
use_ssl = False
use_domain = False
use_token = False
ca = ""
cert = ""
key = ""
ip = ""
to_ip = ""
name = ""
to_name = ""
opcode = 1
op_1_msg = ""
verify = 0
snd_count = 0
stress = False
rcv_count = 0
max_count = 400
import signal
services = ["service-1"]

data = "GET / HTTP/1.1\r\nHost: place\r\nuser-agent: shorty\r\nx-nextensio-for: 127.0.0.1\r\nx-nextensio-uuid: 12345678\r\ncontent-length: xxx\r\n\r\n"

async def aio_readline(greeting):
    line = await asyncio.get_event_loop().run_in_executor(None, sys.stdin.readline)
    return line

async def consumer(msg):
    global op_1_msg, rcv_count

    rcv_count = rcv_count + 1
    if verify:
        if opcode == 2:
            if msg == pdata.d:
                print(f"> matched")
            else:
                print(f"> didn't matched")
        if opcode == 1:
            if msg.decode('utf-8') == op_1_msg:
                print(f"> matched")
            else:
                print(f"> didn't matched")
    print(f"> {msg}")

async def producer():
    global snd_count, stress, max_count
    if not stress or snd_count > max_count:
        msg = await aio_readline(prefix)
    else:
        msg = "hello" + str(snd_count)
    snd_count = snd_count + 1
    if opcode == 2:
        return pdata.d
    else:
        return msg

async def consumer_handler(ws):
    while True:
        msg = await ws.recv()
        await consumer(msg)

async def producer_handler(ws):
    global data, op_1_msg, stress

    if stress:
        await asyncio.sleep(1)
    while True:
        body = await producer()
        if opcode == 1:
            new_body = "<body>\r\n" + body + "</body>\r\n"
            new_data = data
            new_data = re.sub("xxx", str(len(new_body)), new_data, re.MULTILINE)
            msg = new_data + new_body
            op_1_msg = msg
            await ws.send(msg)
        else:
            await ws.send(body)

def read_args():
    global data, url, ca, cert, key, ip, to_ip
    global name, to_name, use_ssl, use_domain, use_token, opcode, verify
    global stress, max_count, services

    parser = argparse.ArgumentParser(description="Configure mode for " + __file__)
    parser.add_argument('--ip', type=str, default="127.0.0.1", help='ip address to connect')
    parser.add_argument('--to_ip', type=str, default="127.0.0.1", help='ip address to send')
    parser.add_argument('--service', type=str, default="abc.com", help='service info')
    parser.add_argument('--prefix', type=str, default="front", help='type of client')
    parser.add_argument('--port', type=int, default=8002, help='port to connect')
    parser.add_argument('--ssl', action='store_true', help='use ssl')
    parser.add_argument('--ca', type=str, default='./ingress_ca.pem', help='path for CA')
    parser.add_argument('--cert', type=str, default='./ingress_client_cert.pem', help='path for cert')
    parser.add_argument('--key', type=str, default='./ingress_client_key.pem', help='path for key')
    parser.add_argument('--token', action='store_true', help='use token')
    parser.add_argument('--domain', action='store_true', help='use domain name')
    parser.add_argument('--name', type=str, default="gateway.sjc.nextensio.net", 
                        help='name to connect')
    parser.add_argument('--to_name', type=str, default="abc.com", help='name to send')
    parser.add_argument('--opcode', type=int, default=1, help='websocket opcode to test')
    parser.add_argument('--verify', action='store_true', help='verify the message in loopback mode')
    parser.add_argument('--stress', action='store_true', help='stress it')
    parser.add_argument('--count', type=int, default=400, help='max packet to send while stressing')
    parser.add_argument('--services', type=str, nargs="*", default=["service-1"], help='services to register')
    args = parser.parse_args()
    port = args.port
    use_ssl = args.ssl
    use_token = args.token
    ip = args.ip
    to_ip = args.to_ip
    use_domain = args.domain
    name = args.name
    to_name = args.to_name
    ca = args.ca
    cert = args.cert
    key = args.key
    opcode = args.opcode
    verify = args.verify
    stress = args.stress
    max_count = args.count
    print(opcode)
    if use_ssl:
        url = "wss://"
    else:
        url = "ws://"
    if use_domain:
        url = url + name
    else:
        url = url + ip
    url = url + ":" + str(port)
    headers['x-nextensio-connect'] = args.service
    prefix = args.prefix
    if use_domain:
        data = re.sub(ip_addr_regex, to_name, data, re.MULTILINE)
    else:
        data = re.sub(ip_addr_regex, to_ip, data, re.MULTILINE)
    data = re.sub("shorty", __file__, data, re.MULTILINE)
    data = re.sub("place", name, data, re.MULTILINE)
    #print(data)
    services = args.services
    print(services)

async def ws_connect(url):
    print(url)
    if use_ssl:
        ssl_context = ssl.SSLContext(ssl.PROTOCOL_TLSv1_2)
        ssl_context.verify_mode = ssl.CERT_REQUIRED
        ssl_context.check_hostname = False
        ssl_context.load_verify_locations(ca)
        ssl_context.load_cert_chain(certfile=cert, keyfile=key)
    else:
        ssl_context = None
    async with websockets.connect(url, extra_headers=headers, ssl=ssl_context) as ws:
        await ws.send("NCTR " + ''.join(services))
        greeting = await ws.recv()
        print(f"< {greeting}")
        c_task = asyncio.ensure_future(consumer_handler(ws))
        p_task = asyncio.ensure_future(producer_handler(ws))
        done, pending = await asyncio.wait([c_task, p_task], 
                            return_when=asyncio.FIRST_COMPLETED,)
        for task in pending:
            task.cancel()

def ws_connect_sync(url):
    print(url)
    ws = create_connection(url, header=headers)
    #peer_ip = ws.sock.getpeername()[0]
    print(ws.sock.getpeername())
    #
    # complete handshake
    #
    ws.send("NCTR " + ''.join(services))
    result = ws.recv()
    print(result)
    return ws

def sig_handler(_signo, _stack_frame):
    global snd_count, rcv_count
    print(f"snd_count {snd_count}, rcv_count {rcv_count}")
    sys.exit(0)

if __name__ == "__main__":
    try:
        logger = logging.getLogger('websockets.server')
        #logging.basicConfig(stream=sys.stdout, level=logging.DEBUG)
        logging.basicConfig(stream=sys.stdout, level=logging.INFO)
        signal.signal(signal.SIGINT, sig_handler)
        read_args()
        if use_token:
            tk = mytoken.get_token()
            if tk != "error":
                headers["Authorization"] = "Bearer " + tk
            else:
                print("Couldn't get token")
                exit(1)
        asyncio.get_event_loop().run_until_complete(ws_connect(url))
    finally:
        pass
