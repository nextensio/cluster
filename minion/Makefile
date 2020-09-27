#
# Author: Davi Gupta (davigupta@gmail.com), Sep 2020
#

VERSION=0.80
NAME=minion
USER=registry.gitlab.com/nextensio/cluster
image=$(shell docker images $(USER)/$(NAME):$(VERSION) -q)
acontid=$(shell docker ps -a --filter ancestor=$(USER)/$(NAME):$(VERSION) -q)

all:
	rm -r -f files/src/version
	echo $(VERSION) > files/src/version
	docker build -t $(USER)/$(NAME):$(VERSION) .

debug:
	rm -r -f files/src/version
	echo $(VERSION) > files/src/version
	docker build -f Dockerfile.debug -t $(USER)/$(NAME):$(VERSION) .

clean:
	-docker rm $(acontid)
	-docker rmi $(image)
	-rm -r -f files/src/version
