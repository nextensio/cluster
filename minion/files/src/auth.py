#!/usr/bin/env python3

#
# Davi Gupta, Sep 2020
#

"""
    Call OPA functions for access check. OPA functionality is written in go, so the
    c-shared library is generated, which is called here (libauth.so)
"""

from ctypes import *

lib = cdll.LoadLibrary("./libauth.so")

class GoString(Structure):
    _fields_ = [("p", c_char_p), ("n", c_longlong)]

lib.AuthInit.restype = c_int
lib.GetUsrAttr.argtypes = [GoString]
lib.GetUsrAttr.restype = GoString
lib.AccessOk.argtypes = [GoString, GoString]
lib.AccessOk.restype = c_int

def goAuthInit(log):
    return lib.AuthInit()

def goGetUsrAttr(id, log):
    goId = GoString(id, len(id))
    usr = lib.GetUsrAttr(goId)
    info = usr.p[:usr.n]
    log.info('{} - {}'.format(id, info))
    return info

def goAccessOk(id, usr, log):
    goId = GoString(id, len(id))
    goUsr = GoString(usr, len(usr))
    access = lib.AccessOk(goId, goUsr)
    log.info('{} - {}'.format(id, access))
    return access

if  __name__ == "__main__":
    import sys
    import logging
    logger = logging.getLogger('auth')
    logging.basicConfig(stream=sys.stdout, level=logging.DEBUG)
    uid = b"1234-5678-9abc-defg"
    goAuthInit(logger)
    info = goGetUsrAttr(uid, logger)
    v = goAccessOk(uid, info, logger)
