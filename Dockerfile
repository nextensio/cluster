FROM golang:1.14

MAINTAINER Davi Gupta <davigupta@gmail.com>
COPY files /go
RUN mkdir -p /root/.ssh
RUN chmod +x /go/gitlab.sh
RUN /go/gitlab.sh
WORKDIR /go/src/app
COPY minion.io .
RUN go env -w GOPRIVATE="gitlab.com"
RUN go env -w GO111MODULE="on"
RUN go get -d -v ./... \
    && go install -v ./...
RUN rm /go/gitlab_rsa
RUN mkdir -p authz
RUN mkdir -p authz/app-access
EXPOSE 80/tcp 8002/tcp
CMD /go/run.sh
