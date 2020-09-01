FROM golang:1.13

MAINTAINER Davi Gupta <davigupta@gmail.com>
COPY files .
WORKDIR /go/src/app
COPY minion.io .
RUN go get -d -v ./...
RUN go install -v ./...
EXPOSE 80/tcp 8002/tcp
CMD ["/go/bin/minion.io", "-tunnel", "-consul_dns", "-register", "-ip", "127.0.0.1", "-iport", "80"]
