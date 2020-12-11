#!/usr/bin/env python3

import asyncio
import time

target_host = "gateway.sjc.nextensio.net"
target_port = 80
dummy_host = "gateway.ric.nextensio.net"

data = "GET / HTTP/1.1\r\nHost: %s\r\nuser-agent: main\r\nx-nextensio-for: tom.com\r\nx-nextensio-uuid: 12345678\r\ncontent-length: 23\r\n\r\n<body>\r\nhello\n</body>\r\n" % dummy_host

async def ht():
    myr, myw = await asyncio.open_connection(target_host, target_port)
    myw.write(data.encode())
    #myw.write_eof()
    await myw.drain()
    time.sleep(.1)
    myw.close()

if __name__ == '__main__':
    loop = asyncio.get_event_loop()
    loop.run_until_complete(ht())
    loop.close()
