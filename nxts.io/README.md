# minion
Humble workers

Put pre-commit file in .git/hooks/pre-commit

Environment

mkdir -p ${HOME}/work/go/src
export GOPATH=${HOME}/work/go
cd ${HOME}/work
git clone git@gitlab.com:nextensio/cluster.git
cd ${HOME}/work/go/src
ln -s ${HOME}/work/cluster/nxts.io/minion.io
go build

# Running test for a particular module
cd nxts.io/minion.io/router
go test -v

# Running a program
cd nxts.io/minion.io
go run minion.go

Debug

Use make debug to generate a debug version of container. It does not run
minion code by default. We need to start it manually.

To run container in local environment

docker run --net host -it davigupta/minion:1.00 /go/bin/minion.io -tunnel
