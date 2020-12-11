#!/usr/bin/env python3

import socket
import time

target_host = "gateway.sjc.nextensio.net"
target_port = 80

data = "GET / HTTP/1.1\r\nHost: %s\r\nuser-agent: main\r\nx-nextensio-for: tom.com\r\nx-nextensio-uuid: 12345678\r\ncontent-length: 5\r\n\r\nhello" % target_host

s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.connect((target_host, target_port))
s.sendall(data.encode())
time.sleep(.1)
s.shutdown(socket.SHUT_WR)
#r = s.recv(1024)
s.close()
