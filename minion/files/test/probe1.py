#!/usr/bin/env python3

#
# Davi Gupta, May 2020
#

import os
import asyncio
import json
import queue
import logging
import sys
import pprint
from datetime import datetime
from datetime import timedelta
import io
import time

IN_PORT = 80

data=b'GET / HTTP/1.1\r\nHost: gateway.sjc.nextensio.net\r\nuser-agent: ./shorty.py\r\nx-nextensio-for: connector-7\r\nx-nextensio-uuid: 12345678\r\ncontent-length: 23\r\n\r\n<body>\r\nhello\n</body>\r\n'

writer = {}
reader = {}
rt = "connector-7"

async def route_http_pak(pak, counter):
    if type(pak) is str:
        npak = pak.encode('utf-8')
    else:
        npak = pak
    con  = asyncio.open_connection("candy-com-in.default.svc.cluster.local", IN_PORT)
    myr, myw = await con
    time.sleep(0.1)
    writer[rt] = myw
    writer[rt].is_closed = False
    reader[rt] = myr
    pak_len = len(npak)
    writer[rt].now = datetime.now()
    writer[rt].write(npak)
    try:
        await writer[rt].drain()
    except (ConnectionResetError, ConnectionAbortedError) as e:
        writer[rt].is_closed = True
        writer[rt].close()
    log.info("[{}] send data to inside pod {}".format(counter, pak_len))

if __name__ == '__main__':
    try:
        pp = pprint.PrettyPrinter(indent=4)
        logging.basicConfig(stream=sys.stdout, level=logging.DEBUG)
        #base_file = os.path.basename(__file__)
        #logging.basicConfig(filename=f"./{base_file}.log", level=logging.INFO,
        #                    format='%(asctime)s %(levelname)-8s %(message)s',
        #                    datefmt='%Y-%m-%d %H:%M:%S')
        log = logging.getLogger()
        task = route_http_pak(data, 1)
        asyncio.ensure_future(task)
        asyncio.get_event_loop().set_debug(True)
        asyncio.get_event_loop().run_forever()
    finally:
        log.info("exiting")
        print("exiting")
