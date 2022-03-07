VERSION=latest
NAME=minion
USER=registry.gitlab.com/nextensio/cluster
image=$(shell docker images $(USER)/$(NAME):$(VERSION) -q)

.PHONY: all
all: build slim

.PHONY: build
build:
	rm -r -f files/version
	echo $(VERSION) > files/version
	cp ~/.ssh/gitlab_rsa files/
	docker build -f Dockerfile.build -t $(USER)/$(NAME):$(VERSION) .
	docker create $(USER)/$(NAME):$(VERSION)
	rm files/gitlab_rsa

.PHONY: slim
slim:
	rm -r -f files/version
	echo $(VERSION) > files/version
	docker build -f Dockerfile.slim -t $(USER)/$(NAME):$(VERSION) .

.PHONY: clean
clean:
	-rm -r -f files/version
