#!/usr/bin/env python3

#
# Author: Davi Gupta (davigupta@gmail.com), Sep 2020
#

import os
import logging
import minion

base_file = os.path.basename(__file__)
logging.basicConfig(filename=f"./{base_file}.log", level=logging.DEBUG)
log = logging.getLogger()
minion.get_environ(log=log)
minion.my_info['register'] = True
minion.cleanup(log=log)
