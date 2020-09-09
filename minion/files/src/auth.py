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
    when the first client joins. The following API is implented in go library.
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
    Task
        This task is run in a seperate thread. Signalling to stop the task is done
        via StopTask. This RunTask should look for stop signal and on true stops
        processing.
        RunTask - args: none
        StopTask - args: none
"""

import sys
import threading
import time
from ctypes import *

lib = cdll.LoadLibrary("./libauth.so")

THREADS = []

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

def goGetUsrAttr(pod, id, log):
    if pod == b"connector":
        return None
    goId = GoString(id, len(id))
    usr = lib.GetUsrAttr(goId)
    info = usr.p[:usr.n]
    log.info('{} - {}'.format(id, info))
    return info

def goAccessOk(pod, id, usr, log):
    if pod == b"agent":
        return 1
    goId = GoString(id, len(id))
    goUsr = GoString(usr, len(usr))
    access = lib.AccessOk(goId, goUsr)
    log.info('{} - {}'.format(id, access))
    return access

def goRunTask():
    global THREADS
    goThread = threading.Thread(target=lib.RunTask)
    THREADS.append(goThread)
    goThread.start()

def goStopTask():
    lib.StopTask()

if  __name__ == "__main__":
    import sys
    import logging
    logger = logging.getLogger('auth')
    logging.basicConfig(stream=sys.stdout, level=logging.DEBUG)
    uid = b"1234-5678-9abc-defg"
    goAuthInit(b"blue", logger)
    goRunTask()
    goUsrJoin(b"agent", uid, logger)
    info = goGetUsrAttr(b"connector", uid, logger)
    if info is None:
        logger.info("empty usr attr")
    info = goGetUsrAttr(b"agent", uid, logger)
    v = goAccessOk(b"connector", uid, info, logger)
    goUsrLeave(b"agent", uid, logger)
    time.sleep(120)
    goStopTask()
    for p in THREADS:
        p.join()
