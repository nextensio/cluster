#!/usr/bin/env python3

#
# Author: Davi Gupta (davigupta@gmail.com), Sep 2020
#

"""
    Send dropped packet do null pod for further inspection and generating stats
"""
import asyncio
import re

OPENED = False
MYW = 0

async def pak_drop(pak, reason, log):
    global OPENED, MYW

    log.error("packet drop: {}".format(reason))
    top, bottom = pak.split(b'\r\n\r\n', 1)
    pak = top +  b'\r\n' + b'x-nextensio-drop: ' + reason.encode('utf-8') + b'\r\n\r\n'
    pak = re.sub(b"content-length: .*", b"content-length: 0", pak, re.MULTILINE)
    if not OPENED:
        try:
            _, MYW = await asyncio.open_connection("blackhole", 10000)
            OPENED = True
        except :
            return
    MYW.write(pak)
    try:
        await MYW.drain()
    except (ConnectionResetError, ConnectionAbortedError) as e:
        MYW.close()
        OPENED = False

async def run_test(count, log):
    pak = b"GET / HTTP/1.1\r\nHost: gateway.sjc.nextensio.net\r\nuser-agent: shorty\r\nx-nextensio-for: 127.0.0.1\r\nx-nextensio-uuid: 123\r\ncontent-length: 23\r\n\r\n<body>\r\nhello\r\n<\body>"
    for i in range(0, count):
        await pak_drop(pak, "access-denied", log)

if  __name__ == "__main__":
    import sys
    import logging
    logger = logging.getLogger('stats')
    logging.basicConfig(stream=sys.stdout, level=logging.DEBUG)
    try:
        task = run_test(2, logger)
        asyncio.ensure_future(task)
        asyncio.get_event_loop().run_forever()
    finally:
        logger.info("exiting")
        print("exiting")
