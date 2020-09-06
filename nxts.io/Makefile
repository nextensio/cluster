#
# Author: Davi Gupta (davigupta@gmail.com), Sep 2020
#

VERSION=1.00
NAME=minion
USER=davigupta
image=$(shell docker images $(USER)/$(NAME):$(VERSION) -q)

all:
	rm -r -f files/version
	echo $(VERSION) > files/version
	docker build -t $(USER)/$(NAME):$(VERSION) .

clean:
	-docker rmi $(image)
	-rm -r -f files/version
