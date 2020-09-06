#
# Author: Davi Gupta (davigupta@gmail.com), Sep 2020
#

all: subsystem

subsystem:
	$(MAKE) -C minion
	$(MAKE) -C nxts.io
	$(MAKE) -C mocklib

clean:
	$(MAKE) -C minion clean
	$(MAKE) -C nxts.io clean
	$(MAKE) -C mocklib clean
