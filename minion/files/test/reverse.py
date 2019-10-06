#!/usr/bin/env python3
# 
# Davi Gupta, Mar 2019
#

import asyncio
import websockets
import json
import http.client
import logging
import sys

headers = {'x-nextensio-codec': 'text',
           'x-nextensio-connect': 'abc.com'}

async def aio_readline(greeting):
    line = await asyncio.get_event_loop().run_in_executor(None, sys.stdin.readline)
    return line

async def consumer(message):
    print(f"> {message}")

async def producer():
    message = await aio_readline("reverse")
    return message

async def consumer_handler(websocket):
    print("I'm consumer")
    while True:
        message = await websocket.recv()
        await consumer(message)

async def producer_handler(websocket):
    print("I'm producer")
    while True:
        message = await producer()
        await websocket.send(message)

async def hello():
    async with websockets.connect(
            'ws://egress.mesh.nextensio.net', extra_headers=headers) as websocket:
        print(websocket.request_headers['x-nextensio-codec'])
        await websocket.send("nextensio")
        greeting = await websocket.recv()
        print(f"< {greeting}")
        consumer_task = asyncio.ensure_future(
                            consumer_handler(websocket))
        producer_task = asyncio.ensure_future(
                            producer_handler(websocket))
        done, pending = await asyncio.wait(
                            [consumer_task, producer_task], 
                            return_when=asyncio.FIRST_COMPLETED,)
        for task in pending:
            task.cancel()

if __name__ == "__main__":
    logger = logging.getLogger('websockets.server')
    logging.basicConfig(stream=sys.stdout, level=logging.DEBUG)
    asyncio.get_event_loop().run_until_complete(hello())
