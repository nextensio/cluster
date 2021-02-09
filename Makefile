#
# Author: Davi Gupta (davigupta@gmail.com), Sep 2020
#

VERSION=1.10
NAME=minion
USER=registry.gitlab.com/nextensio/cluster
image=$(shell docker images $(USER)/$(NAME):$(VERSION) -q)
bimage=$(shell docker images $(USER)/$(NAME)-build:$(VERSION) -q)
dimage=$(shell docker images $(USER)/$(NAME)-debug:$(VERSION) -q)
acontid=$(shell docker ps -a --filter ancestor=$(USER)/$(NAME):$(VERSION) -q)
abcontid=$(shell docker ps -a --filter ancestor=$(USER)/$(NAME)-build:$(VERSION) -q)
adcontid=$(shell docker ps -a --filter ancestor=$(USER)/$(NAME)-debug:$(VERSION) -q)
bcontid=$(shell docker ps -a --filter ancestor=$(USER)/$(NAME)-build:$(VERSION) -q | head -n 1)

.PHONY: all
all: build slim

.PHONY: build
build:
	rm -r -f files/version
	echo $(VERSION) > files/version
	cp ~/.ssh/gitlab_rsa files/
	docker build -f Dockerfile.build -t $(USER)/$(NAME)-build:$(VERSION) .
	docker create $(USER)/$(NAME)-build:$(VERSION)
	rm files/gitlab_rsa

.PHONY: slim
slim:
	rm -r -f files/version
	echo $(VERSION) > files/version
	docker build -f Dockerfile.slim -t $(USER)/$(NAME):$(VERSION) .

.PHONY: clean
clean:
	-docker rm $(acontid)
	-docker rm $(abcontid)
	-docker rm $(adcontid)
	-docker rmi $(image)
	-docker rmi $(bimage)
	-docker rmi $(dimage)
	-rm -r -f files/version
