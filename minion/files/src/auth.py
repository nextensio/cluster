#!/usr/bin/env python3

#
# Author: Davi Gupta (davigupta@gmail.com), Sep 2020
#

"""
    Call OPA functions for access check. OPA functionality is written in go, so the
    c-shared library is generated, which is called here (libauth.so)
    A given POD only services client of a particular tenant identified by the namespace.
    Also a POD serves only one type of clients either agent or connector and not mix. When
    the POD is spawned it knows which namespace it belongs. Type of client binding is done
    when the first client joins. The following API is called in following init.
    Init phase:
        AuthInit - args : namespace
    User Join
        UsrJoin - args : client type {"agent", "connector"}, client uuid
    Packet Fowrarding
        Namespace can be obtained as the library was initialised with that info
        This function is only called if client type is "agent"
        GetUserAttr - args: client uuid, return: json formatted user string
        This function is only called if client type is "connector"
        AccessOk - args: client uuid, return: boolean {1: permit, 0: deny}
    User Leave
        UsrLeave - args : client type {"agent", "connector"}, client uuid
    Task: TODO
        Plan is to create a thread where is library background task can be run
"""

from ctypes import *

lib = cdll.LoadLibrary("./libauth.so")

class GoString(Structure):
    _fields_ = [("p", c_char_p), ("n", c_longlong)]

lib.AuthInit.argtypes = [GoString]
lib.AuthInit.restype = c_int
lib.UsrJoin.argtypes = [GoString, GoString]
lib.UsrLeave.argtypes = [GoString, GoString]
lib.GetUsrAttr.argtypes = [GoString]
lib.GetUsrAttr.restype = GoString
lib.AccessOk.argtypes = [GoString, GoString]
lib.AccessOk.restype = c_int

def goAuthInit(ns, log):
    goNs = GoString(ns, len(ns))
    return lib.AuthInit(goNs)

def goUsrJoin(pod, id, log):
    goId = GoString(id, len(id))
    goPod = GoString(pod, len(pod))
    usr = lib.UsrJoin(goPod, goId)

def goUsrLeave(pod, id, log):
    goId = GoString(id, len(id))
    goPod = GoString(pod, len(pod))
    usr = lib.UsrLeave(goPod, goId)

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

def goRunTask():
    lib.RunTask()

if  __name__ == "__main__":
    import sys
    import logging
    logger = logging.getLogger('auth')
    logging.basicConfig(stream=sys.stdout, level=logging.DEBUG)
    uid = b"1234-5678-9abc-defg"
    goAuthInit(b"blue", logger)
    goUsrJoin(b"agent", uid, logger)
    info = goGetUsrAttr(uid, logger)
    v = goAccessOk(uid, info, logger)
    goUsrLeave(b"agent", uid, logger)
