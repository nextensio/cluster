#!/usr/bin/env python3

#
# Author: Davi Gupta (davigupta@gmail.com), Apr 2019
#

import re

len_regex_b = re.compile(b'content-length:\s?(.*)\r\n', re.IGNORECASE)

class HttpParser(object):
    def __init__(self):
        # protected variables
        self._headers = []
        self._body = []
        self._buf = []
        self._clen = 0
        self._parse_len = 0

        # private events
        self.__on_headers_complete = False
        self.__on_message_complete = False
        self.__cursor = 0

    def get_headers(self):
        return b''.join(self._headers)

    def get_body(self):
        return b''.join(self._body)

    def get_clen(self):
        return self._clen

    def is_headers_complete(self):
        return self.__on_headers_complete

    def is_message_complete(self):
        return self.__on_message_complete

    def _parse_headers(self, data):
        idx = data.find(b'\r\n\r\n')
        if idx < 0:
            return False
        self._headers.append(data[:idx+4])
        rest = data[idx+4:]
        self._buf = [rest]
        m = len_regex_b.search(self._headers[0])
        if m == None:
            self._clen = 0
        else:
            self._clen = int(m[1])
        self.__on_headers_complete = True
        self._parse_len = idx + 4 + self._clen
        return True

    def _parse_body(self):
        body_part = b''.join(self._buf)
        if self._clen  <= len(body_part):
            self._body.append(body_part[:self._clen])
            self.__on_message_complete = True
            self._partial_body = False
            return True
        else:
            self._partial_body = True
            return False

    def execute(self, data, length):
        """
            If message parsing is complete then reset everything.
            Given data may have more than one http packets, but
            just parse the first one.
        """
        if self.__on_message_complete:
            self.__init__()
        while True:
            if not self.__on_headers_complete:
                if data:
                    self._buf.append(data)
                    data = b''
                try:
                    to_parse=b''.join(self._buf)
                    ret = self._parse_headers(to_parse)
                    if not ret:
                        self.__cursor += length
                        return length
                except:
                    pass
            elif not self.__on_message_complete:
                if data:
                    self._buf.append(data)
                    data = b''
                ret = self._parse_body()
                if not ret:
                    self.__cursor += length
                    return length
            else:
                return self._parse_len - self.__cursor
