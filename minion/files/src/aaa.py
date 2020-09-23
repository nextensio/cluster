#!/usr/bin/env python3

#
# Author: Davi Gupta (davigupta@gmail.com), Sep 2020
#

"""
    Call OPA functions for access check. OPA functionality is written in go, so the
    c-shared library is generated, which is called here (libaaa.so)
    A given POD only services client of a particular tenant identified by the namespace.
    Also a POD serves only one type of clients either agent or connector and not mix. When
    the POD is spawned it knows which namespace it belongs. Type of client binding is done
    when the first client joins. The following API is implented in go library.
    Init phase:
        AaaInit - args : namespace, uri
    User Allowed
        UsrAllowed - args : client uuid,
                     return: boolean {1: permit, 0: deny}
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

import os
import sys
import threading
import time
from ctypes import *
from ctypes.util import find_library

if os.environ.get('MY_SIM_TEST') :
    lib = cdll.LoadLibrary("./libaaa_mock.so")
else:
    lib = cdll.LoadLibrary("./libaaa.so")

THREADS = []

class GoString(Structure):
    _fields_ = [("p", c_char_p), ("n", c_longlong)]

lib.AaaInit.argtypes = [GoString, GoString]
lib.AaaInit.restype = c_int
lib.UsrAllowed.argtypes = [GoString]
lib.UsrAllowed.restype = c_int
lib.UsrJoin.argtypes = [GoString, GoString]
lib.UsrLeave.argtypes = [GoString, GoString]
lib.GetUsrAttr.argtypes = [GoString]
lib.GetUsrAttr.restype = c_char_p
lib.AccessOk.argtypes = [GoString, GoString]
lib.AccessOk.restype = c_int
lib.RouteLookup.argtypes = [GoString, GoString]
lib.RouteLookup.restype = c_char_p

def goAaaInit(ns, uri, log):
    goNs = GoString(ns, len(ns))
    goUri = GoString(uri, len(uri))
    return lib.AaaInit(goNs, goUri)

def goUsrAllowed(id, log):
    goId = GoString(id, len(id))
    usr = lib.UsrAllowed(goId)
    if usr:
        return True
    else:
        return False

def goUsrJoin(pod, id, log):
    if pod == b"connector":
        return
    goId = GoString(id, len(id))
    goPod = GoString(pod, len(pod))
    usr = lib.UsrJoin(goPod, goId)

def goUsrLeave(pod, id, log):
    if pod == b"connector":
        return
    goId = GoString(id, len(id))
    goPod = GoString(pod, len(pod))
    usr = lib.UsrLeave(goPod, goId)

def goGetUsrAttr(pod, id, log):
    if pod == b"connector":
        return None
    goId = GoString(id, len(id))
    info = lib.GetUsrAttr(goId)
    log.info('{} - {}'.format(id, info))
    return info

def goAccessOk(pod, id, usr, log):
    if pod == b"agent":
        return 1
    goId = GoString(id, len(id))
    goUsr = GoString(usr, len(usr))
    access = lib.AccessOk(goId, goUsr)
    log.info('{} - {}'.format(id, access))
    if not access:
        log.info('{}'.format(usr))
    return access

def goRouteLookup(id, route, log):
    goId = GoString(id, len(id))
    goRoute = GoString(route, len(route))
    tag = lib.RouteLookup(goId, goRoute)
    log.info('Route {} - {}'.format(route, tag))
    if tag == b'':
        return None
    return tag

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
    logger = logging.getLogger('aaa')
    logging.basicConfig(stream=sys.stdout, level=logging.DEBUG)
    # 5f57d00ca712c68fb308e020	123	John Doe	johndoe@gmail.com
    uid = b"123"
    bid = b"923"
    uri = b"mongodb+srv://nextensio:nextensio238@cluster0.prph0.mongodb.net"
    goAaaInit(b"blue", uri, logger)
    goRunTask()
    goUsrAllowed(uid, logger)
    goUsrJoin(b"agent", uid, logger)
    goUsrJoin(b"connector", bid, logger)
    tag = goRouteLookup(uid, b"connector-12", logger)
    if tag is None:
        logger.info("tag is empty")
    info = goGetUsrAttr(b"connector", uid, logger)
    if info is None:
        logger.info("empty usr attr")
    info = goGetUsrAttr(b"agent", uid, logger)
    #5f57d00ca712c68fb308e020	923	Accounting
    v = goAccessOk(b"connector", bid, info, logger)
    goUsrLeave(b"agent", uid, logger)
    goUsrLeave(b"connector", bid, logger)
    time.sleep(120)
    goStopTask()
    for p in THREADS:
        p.join()
