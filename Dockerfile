FROM golang:1.13

MAINTAINER Davi Gupta <davigupta@gmail.com>
COPY files /go
WORKDIR /go/src/app
COPY minion.io .
RUN go get -d -v ./... \
    && go install -v ./...
RUN mkdir -p authz
RUN mkdir -p authz/app-access
EXPOSE 80/tcp 8002/tcp
CMD ["tail -f /dev/null"]
