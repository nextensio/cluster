#
# Author: Davi Gupta (davigupta@gmail.com), Sep 2020
#

VERSION=0.75
NAME=minion
USER=davigupta
image=$(shell docker images $(USER)/$(NAME):$(VERSION) -q)

all:
	rm -r -f files/src/version
	echo $(VERSION) > files/src/version
	docker build -t $(USER)/$(NAME):$(VERSION) .

clean:
	-docker rmi $(image)
	-rm -r -f files/src/version
