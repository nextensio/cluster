#!/usr/bin/env python3

#
# Author: Davi Gupta (davigupta@gmail.com), Mar 2019
#

"""
   Workers of Nextensio world. They work tirelessly connecting two islands
   in a secure way. Let the work begin.
   Worker listens on two ports:
   * 8001 : for worker-to-worker communication
   * 8002 : for islands to connect to the worker
   communication happens over websocket. Format of data exchanged over websocket
   is json or http/1.1 or http/2 formatted.
"""

import os
import asyncio
import websockets
import json
import queue
import logging
import sys
import argparse
import pprint
from datetime import datetime
from datetime import timedelta
import io
import re
import consul.aio
from async_dns import types
from async_dns.resolver import ProxyResolver
import requests
import json
from collections import OrderedDict
import signal
import time
import ipaddress
import traceback
from myparse import HttpParser
import auth

OUT_PORT = 8002
IN_PORT = 8001
HOST = "192.168.1.113"
READ_BYTES = 256

#
# use fully qualified domain name i.e., name ending with "."
#

ip_addr_regex = re.compile(r'\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b')
frame_mode = False
tasks = {}
handles = {}
queues = {}
writer = {}
reader = {}
codec = "text"
tunnel = False
suffix = ".svc.cluster.local"
comps = []
IDLE_TIME = 10
SLEEP_TIME = 20
fwd = {}
use_consul_http = False
use_consul_dns = False
my_info = {}
g1_suffix = "gateway."
g2_suffix = ".nextensio.net"
pend = []
pend_now = datetime.now()
for_regex_b = re.compile(b'x-nextensio-for:\s?(.*)\r\n', re.IGNORECASE)
for_regex_s = re.compile(r'x-nextensio-for:\s?(.*)\r\n', re.IGNORECASE)
len_regex_b = re.compile(b'content-length:\s?(.*)\r\n', re.IGNORECASE)
usr_regex_b = re.compile(b'x-nextensio-attr:\s?(.*)\r\n', re.IGNORECASE)
my_info['services'] = []
UUID = 0
CLITYPE = "agent"

def valid_ip(address):
    try:
        ipaddress.ip_address(address)
        return True
    except:
        return False

def close_internal():
    log.info("cleanup handler for internal connections")
    delete = []
    for k in writer:
        writer[k].close()
        writer[k].is_closed = True
        log.info("connection closed for {}".format(k))
        delete.append(k)
    for i in delete:
        del writer[i]
        del reader[i]
    while pend:
        i = pend.pop(0)
        i.close()

def register_to_consul():
    """ 
        Add KV pairs viz., cluster:<>, pod:<>
        Add Service definition
    """

    if my_info['test']:
        return
    if not my_info['register']:
        return

    payload = open('service.json', 'r')
    obj = json.load(payload, object_pairs_hook=OrderedDict)
    services = my_info['services']
    for i in services:
        h = re.sub("\.", "-", i)
        url = 'http://' + my_info['node'] + '.node.consul:8500/v1/kv/' + h + '-' + my_info['namespace']
        log.debug(url)
        data = my_info['id']
        r = requests.put(url + '/cluster', data=data, verify=False)
        pod = re.sub("\.", "-", my_info['pod'])
        data = pod
        r = requests.put(url + '/pod', data=data, verify=False)
        obj['ID'] = h + '-' + my_info['namespace']
        obj['Name'] = h + '-' + my_info['namespace']
        obj['Address'] = my_info['ip']
        obj['Meta']['cluster'] = my_info['id']
        obj['Meta']['pod'] = pod
        headers = {'content-type': 'application/json', 'Accpet-Charset': 'UTF-8'}
        url = 'http://' + my_info['node'] + '.node.consul:8500/v1/agent/service/register'
        log.debug(url)
        r = requests.put(url, data=json.dumps(obj), headers=headers, verify=False)

def deregister_from_consul(log):
    """ 
        Remove KV pairs viz., cluster:<>, pod:<>
        Remove Service definition
    """

    if my_info['test']:
        return
    if not my_info['register']:
        return

    services = my_info['services']
    for i in services:
        h = re.sub("\.", "-", i)
        url = 'http://' + my_info['node'] + '.node.consul:8500/v1/kv/' + h + '-' + my_info['namespace']
        log.debug(url)
        r = requests.delete(url + '/cluster', verify=False)
        r = requests.delete(url + '/pod', verify=False)
        url = 'http://' + my_info['node'] + '.node.consul:8500/v1/agent/service/deregister/' + h + '-' + my_info['namespace']
        log.debug(url)
        r = requests.put(url, verify=False)

async def consul_http_lookup(name):
    log.info("consul http lookup for {}".format(name))
    c = consul.aio.Consul(host=my_info['c_server'], port=my_info['c_port'],
                          token=None, scheme=my_info['c_scheme'], 
                          consistency='default', verify=False, dc=None, cert=None)
    if not c:
        return None
    index = None
    index, data = await c.kv.get(name, index=index, recurse=True)
    if data is not None:
        fwd['id'] = data[0]['Value'].decode('utf-8')
        fwd['igw'] = data[1]['Value'].decode('utf-8')
        await c.http._session.close()
        if fwd['id'] == my_info['id']:
            fwd['local'] = True
            return fwd['igw'] + '-in.' + my_info['namespace'] + suffix
        else:
            fwd['local'] = False
            return g1_suffix + fwd['id'] + g2_suffix
    else:
        return None

async def consul_dns_lookup(name):
    n_name = name + ".query.consul"
    log.info("consul dns lookup for {}".format(n_name))
    f = None
    r = ProxyResolver()
    r.set_proxies([my_info['ns']])
    ans = await r.query(n_name, qtype=types.SRV)
    if ans is None:
        return None
    for i in ans:
        f = i.data[-1].split('.')[-2]
        break;
    if f is not None:
        if f == my_info['id']:
            s = await consul_http_lookup(name)
            return s
        else:
            fwd['local'] = False
            return g1_suffix + f + g2_suffix
    else:
        return None

class BridgeProtocol(asyncio.Protocol):
    def connection_made(self, transport):
        peername = transport.get_extra_info('peername')
        log.info('Connection from {}'.format(peername))
        self.transport = transport
        self.counter = 0

    def data_received(self, data):
        self.counter = self.counter + 1
        log.info("[{}] sending data to ws {}".format(self.counter, len(data)))
        queues["outside"].put_nowait(data)

class HTTPProtocol(asyncio.Protocol):
    def __init__(self):
        self.ht_parse = HttpParser()
        self.header_done = False
        self.peername = ""
        self.transport = None
        self.counter = 0
        log.debug("http parser init")

    def connection_made(self, transport):
        self.peername = transport.get_extra_info('peername')
        log.info('Connection from {}'.format(self.peername))
        self.transport = transport

    def send_ok(self):
        """
        Ack the HTTP request
        Reply as HTTP/1.1 server, saying HTTP OK
        """
        resp_headers = {
            'Content-Length': 0,
        }
        resp_headers_raw = ''.join('%s: %s\n'%(k,v) for k, v in resp_headers.items())
        resp_proto = 'HTTP/1.1'.encode()
        resp_status = '200'.encode()
        resp_status_text = 'OK'.encode()
        self.transport.write(b'%s %s %s\n'%(resp_proto, resp_status, resp_status_text))
        self.transport.write(resp_headers_raw.encode())
        self.transport.write(b'\n')
        log.info("sending ack")

    def data_received(self, data):
        log.debug("http data received")
        log.debug(data)
        new_data = data
        data_len = len(new_data)
        while data_len:
            nparsed = self.ht_parse.execute(new_data, data_len)
            if self.ht_parse.is_headers_complete() and not self.header_done:
                self.header_done = True
            if self.ht_parse.is_message_complete():
                self.counter = self.counter + 1
                log.info("[{}] sending data to ws {}".format(self.counter, self.ht_parse.get_clen()))
                pak = b''+self.ht_parse.get_headers()+self.ht_parse.get_body()
                queues["outside"].put_nowait(pak)
                log.info("queue size {}".format(queues["outside"].qsize()))
                self.header_done = False
                self.send_ok()
            new_data = new_data[nparsed:]
            data_len = data_len - nparsed

    def eof_received(self):
        log.info('Closing HTTP connection for {}'.format(self.peername))
        self.transport.close()

    def connection_lost(self, exc):
        log.info('Connection close for {}'.format(self.peername))
        self.transport.close()

async def route_json_pak(pak, counter, uuid):
    global CLITYPE, UUID
    if CLITYPE == 'agent':
        usr_info = auth.goGetUsrAttr(UUID, log)
    jpak = json.loads(pak)

#
# Instead of parsing HTTP data, just search for x-nextensio-for
#
async def route_http_pak(pak, counter, uuid):
    global CLITYPE, UUID
    #p = HttpParser()
    if type(pak) is str:
        npak = pak.encode('utf-8')
    else:
        npak = pak
    receved = len(npak)
    if CLITYPE == 'agent':
        usr_info = auth.goGetUsrAttr(UUID, log)
        """ insert use info """
        top, mid, bottom = npak.split(b'\r\n', 2)
        npak = top + b'\r\n' + mid + b'\r\n' + b'x-nextensio-attr: ' + usr_info + b'\r\n' + bottom
    #nparsed = p.execute(npak, receved)
    #head = p.get_headers()
    #pp.pprint(head)
    #pp.pprint(p.recv_body())
    #rt = head['x-nextensio-for']
    #if rt == False :
        #return 0
    #print(writer.get(rt))
    if type(pak) is str:
        m = for_regex_s.search(pak)
        if m == None:
            return 0
        rt = m[1]
    else:
        m = for_regex_b.search(pak)
        if m == None:
            return 0
        rt = m[1].decode('utf-8')
   
    if writer.get(rt) == None or writer[rt].is_closed:
        comps = rt.split(':', 1)
        host = comps[0]
        is_ip = valid_ip(host)
        if not is_ip:
            host=re.sub("\.", "-", host)
            consul_key = host + my_info['c_suffix']
            if use_consul_http:
                complete_rt = await consul_http_lookup(consul_key)
                if complete_rt is None:
                    log.error("packet drop: lookup failure")
                    return
            elif use_consul_dns:
                complete_rt = await consul_dns_lookup(consul_key)
                if complete_rt is None:
                    log.error("packet drop: lookup failure")
                    return
            else:
                complete_rt = host + '-in.' + my_info['namespace'] + suffix
                fwd['local'] = True
        else:
            complete_rt = host
            fwd['local'] = True
        log.info(complete_rt)
        fwd['dest'] = complete_rt
        myr, myw = await asyncio.open_connection(complete_rt, IN_PORT)
        #
        # Needs to be debugged why this sleep is needed. Without that TCP
        # connection is not getting established
        #
        time.sleep(0.1)
        writer[rt] = myw;
        writer[rt].is_closed = False
        reader[rt] = myr;
    if not fwd['local']:
        nnpak = re.sub(b'(Host:\s?[a-z.]+\r\n)', b'Host: ' + fwd['dest'].encode('utf-8') + b'\r\n', npak, count=1)
        log.debug(nnpak)
        pak_len = len(nnpak)
        writer[rt].now = datetime.now()
        writer[rt].write(nnpak)
        try:
            await writer[rt].drain()
            resp = await reader[rt].read(READ_BYTES)
            log.debug("got an ack")
            log.debug(resp)
        except (ConnectionResetError, ConnectionAbortedError) as e:
            log.info("Got exception, close connection for {}".format(rt))
            writer[rt].is_closed = True
            writer[rt].close()
    else:
        pak_len = len(npak)
        writer[rt].now = datetime.now()
        writer[rt].write(npak)
        try:
            await writer[rt].drain()
        except (ConnectionResetError, ConnectionAbortedError) as e:
            writer[rt].is_closed = True
            writer[rt].close()
    log.info("[{}] send data to inside pod {}".format(counter, pak_len))

#
# only allow 1 connection to exist at a time -- current limitation
#
async def l_worker(pin, pout, tunnel, websocket, path):
    global CLITYPE, UUID
    log.info(f"listener {pin} in action")
    if handles.get(pin):
        await websocket.close()
        log.info(f"new connection {pin} while older connection exist")
        return
    counter = 0
    UUID = websocket.request_headers['x-nextensio-uuid'].encode('utf-8')
    codec = websocket.request_headers['x-nextensio-codec']
    log.info(codec)
    if tunnel:
        sec = await websocket.recv()
        log.info(f"< {sec}")
        if type(sec) is not str:
            ssec = sec.decode()
        else:
            ssec = sec
        services = ssec.split()
        if services[0] == "NCTR":
            CLITYPE="connector"
        else:
            CLITYPE="agent"
        if services[0] == "NCTR" or services[0] == "NAGT":
            greeting = f"Hello {services[0]}!"
            #
            # register all services to consul
            #
            my_info['services'] = services[1:]
            register_to_consul()
            await websocket.send(greeting)
            log.info(f"> {greeting}")
    auth.goUsrJoin(CLITYPE.encode('utf-8'), UUID, log)
    handles[pin] = websocket
    frame_mode = True
    try:
        while frame_mode:
            pak = await websocket.recv()
            if tunnel:
                if "json" in codec:
                    await route_json_pak(pak, counter, UUID)
                elif "http" in codec:
                    await route_http_pak(pak, counter, UUID)
                await asyncio.sleep(0)
            else:
                await queues[pout].put(pak)
            counter = counter + 1
    except Exception as e:
        tb = sys.exc_info()
        log.info("Closing WS connection - error {}".format(tb[0]))
        handles[pin] = None
        await websocket.close()
        if tunnel:
            #close_internal()
            deregister_from_consul(log)
            my_info['services'] = []
            auth.goUsrLeave(CLITYPE.encode('utf-8'), UUID, log)
            pass

async def in_hello(websocket, path):
    await l_worker("inside", "outside", 0, websocket, path)

async def out_hello(websocket, path):
    await l_worker("outside", "inside", tunnel, websocket, path)

async def q_worker(pin):
    global UUID
    log.info(f"worker queue {pin} -> websocket {pin}")
    while True:
        try:
            log.debug("get next")
            pak = await queues[pin].get()
            log.info(f"got pak, handle {handles.get(pin)} for {pin}")
            access = True
            if pin == "outside" and CLITYPE == "connector":
               " check access "
               m = usr_regex_b.search(pak)
               if m == None:
                   pass
               else:
                   usr = m[1]
                   """ UUID is kept globally, as current implementatio supports
                       only 1 connection either agent or connector
                   """
                   access = auth.goAccessOk(UUID, usr, log)
            if handles.get(pin):
                if access:
                    await handles[pin].send(pak)
            queues[pin].task_done()
        except:
            traceback.print_exc()

async def periodic():
    while True:
        now = datetime.now()
        delete = []
        for k in writer:
            tdelta = now - writer[k].now
            if tdelta > timedelta(minutes = IDLE_TIME):
                writer[k].close()
                #await writer[k].wait_closed()
                writer[k].is_closed = True
                log.info("connection closed for {}".format(k))
                delete.append(k)
        for i in delete:
            del writer[i]
            del reader[i]
        tdelta = now - pend_now
        if tdelta > timedelta(minutes = IDLE_TIME):
            while pend:
                i = pend.pop(0)
                i.close()
        await asyncio.sleep(SLEEP_TIME)

def init_periodic():
    task = periodic()
    asyncio.ensure_future(task)

def init_listener():
    out_server = websockets.serve(out_hello, HOST, OUT_PORT, ping_interval=120, ping_timeout=60)
    asyncio.ensure_future(out_server)
    if not tunnel:
        in_server = websockets.serve(in_hello, HOST, IN_PORT)
        asyncio.ensure_future(in_server)
    else:
        loop = asyncio.get_event_loop()
        #task = loop.create_server(BridgeProtocol, HOST, IN_PORT)
        task = loop.create_server(HTTPProtocol, HOST, IN_PORT, backlog=20)
        asyncio.ensure_future(task)

def init_worker():
    queues["inside"] = asyncio.Queue(maxsize=0)
    queues["outside"] = asyncio.Queue(maxsize=0)
    task = q_worker("outside")
    asyncio.ensure_future(task)
    tasks["outside"] = task
    task = q_worker("inside")
    asyncio.ensure_future(task)
    tasks["inside"] = task

def get_environ(log):
    my_info['node'] = os.environ.get('MY_NODE_NAME')
    my_info['pod'] = os.environ.get('MY_POD_NAME')
    my_info['namespace'] = os.environ.get('MY_POD_NAMESPACE')
    my_info['ip'] = os.environ.get('MY_POD_IP')
    my_info['id']= os.environ.get('MY_POD_CLUSTER')
    my_info['ns']= os.environ.get('MY_DNS')
    my_info['test']= os.environ.get('MY_SIM_TEST')
    log.info(my_info['node'])
    log.info(my_info['pod'])
    log.info(my_info['namespace'])
    log.info(my_info['ip'])
    log.info(my_info['ns'])
    my_info['c_suffix'] = "-" + my_info['namespace']
    my_info['c_server'] = my_info['node'] + ".node.consul"
    #my_info['c_server'] = "consul.kismis.org"
    my_info['c_port'] = 8500
    #my_info['c_port'] = ''
    my_info['c_scheme'] = 'http'
    #my_info['c_scheme'] = 'https'
    log.info(my_info['c_suffix'])
    log.info(my_info['c_server'])

def cleanup(log):
    log.info("cleaning up...")
    print("cleaning up...")
    deregister_from_consul(log=log)

def sig_handler(_signo, _stack_frame):
    cleanup(log=log)
    sys.exit(0)

def handle_pdb(sig, frame):
    import pdb
    pdb.Pdb().set_trace(frame)

def handle_debug(sig, frame):
    if log.level == logging.DEBUG:
        log.setLevel(logging.INFO)
    else:
        log.setLevel(logging.DEBUG)
    log.info("set log level to {}".format(log.level))

if __name__ == '__main__':
    try:
        pp = pprint.PrettyPrinter(indent=4)
        base_file = os.path.basename(__file__)
        logger = logging.getLogger('websockets.server')
        #logging.basicConfig(stream=sys.stdout, level=logging.DEBUG)
        #logging.basicConfig(filename=f"./{base_file}.log", level=logging.DEBUG)
        logging.basicConfig(filename=f"./{base_file}.log", level=logging.INFO,
                            format='%(asctime)s %(levelname)-8s %(message)s',
                            datefmt='%Y-%m-%d %H:%M:%S')
        log = logging.getLogger()
        parser = argparse.ArgumentParser(description="Configure mode for minion")
        parser.add_argument('-t', '--tunnel', action='store_true', help='run in tunnel mode')
        parser.add_argument('--ip', type=str, default="127.0.0.1", help='ip address to listen')
        parser.add_argument('--iport', type=int, default=8001, help='inside port')
        parser.add_argument('--oport', type=int, default=8002, help='outside port')
        parser.add_argument('--consul_http', action='store_true', help='use consul http')
        parser.add_argument('--consul_dns', action='store_true', help='use consul dns')
        parser.add_argument('--register', action='store_true', help='register service to consul')
        args = parser.parse_args()
        tunnel = args.tunnel
        use_consul_http = args.consul_http
        use_consul_dns = args.consul_dns
        my_info['register'] = args.register
        HOST = args.ip
        IN_PORT = args.iport
        OUT_PORT = args.oport
        get_environ(log=log)
        log.info(os.getpid())
        signal.signal(signal.SIGTERM, sig_handler)
        signal.signal(signal.SIGINT, sig_handler)
        signal.signal(signal.SIGUSR1, handle_pdb)
        signal.signal(signal.SIGUSR2, handle_debug)
        auth.goAuthInit(my_info['namespace'].encode('utf-8'), log)
        init_periodic()
        init_listener()
        init_worker()
        #asyncio.get_event_loop().set_debug(True)
        asyncio.get_event_loop().run_forever()
    finally:
        log.info("exiting")
        print("exiting")
