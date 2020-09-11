#!/usr/bin/env python3

#
# Author: Davi Gupta (davigupta@gmail.com), Sep 2020
#

def pak_drop(pak, reason, log):
    log.error("packet drop: {}".format(reason))
