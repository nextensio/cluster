# This file is a template, and might need editing before it works on your project.
image: golang:latest

variables:
  # Please edit to your GitLab project
  REPO_NAME: gitlab.com/nextensio/cluster
# The problem is that to be able to use go get, one needs to put
# the repository in the $GOPATH. So for example if your gitlab domain
# is gitlab.com, and that your repository is namespace/project, and
# the default GOPATH being /go, then you'd need to have your
# repository in /go/src/gitlab.com/namespace/project
# Thus, making a symbolic link corrects this.
before_script:
  - mkdir -p $GOPATH/src
  - ln -svf $CI_PROJECT_DIR/minion.io $GOPATH/src/
  - cd $GOPATH/src/minion.io

stages:
  - test
  - build
  - deploy

format:
  stage: test
  script:
    - go fmt $(go list ./... | grep -v /vendor/)
    - go vet $(go list ./... | grep -v /vendor/)
    - go test -race $(go list ./... | grep -v /vendor/ | grep -v consul)

compile:
  stage: build
  script:
    - go build -race -ldflags "-extldflags '-static'" -o $CI_PROJECT_DIR/mybinary
  artifacts:
    paths:
      - mybinary
