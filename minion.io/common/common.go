/*
 * common.go: variables, constant, structures shared across packages
 *
 * Author: Davi Gupta (davigupta@gmail.com), Jun 2019
 */

package common

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Constants used by this program
const (
	MaxService       = 32
	LocalSuffix      = ".svc.cluster.local"
	RemotePrePrefix  = "gateway."
	RemotePostPrefix = ".nextensio.net"
	PongWait         = 60 * time.Second
	PingPeriod       = (PongWait * 9) / 10
	MaxMessageSize   = 64 * 1024
	WsReadLimit      = 72 * 1024 // MaxMessageSize + 8K
	SelfDest         = 1
	LocalDest        = 2
	RemoteDest       = 3
)

// Structure for storing various parameters for this program
type Params struct {
	Tunnel    bool
	UseDns    bool
	UseHttp   bool
	Register  bool
	Iport     int
	Oport     int
	ListenIp  string
	Node      string
	Pod       string
	Namespace string
	PodIp     string
	Id        string
	DnsIp     string
	MongoUri  string
	Sim       bool
	C_suffix  string
	C_server  string
	C_port    int
	C_scheme  string
}

var MyInfo Params

// Json structure for registering consul service
// Begin
type Meta struct {
	Cluster string
	Pod     string
}

type Entry struct {
	ID      string
	Name    string
	Address string
	Meta    Meta
}

// End

// Json structure for consul key-value result
// Begin
type Consul struct {
	Kvs []Kv
}

type Kv struct {
	LockIndex   int
	Key         string
	Flags       int
	Value       string
	CreateIndex int
	ModifyIndex int
}

// End

// Structure for storing forwarding result
type Fwd struct {
	DestType int
	Pod      string
	Id       string
	Dest     string
}

func HttpToBytes(s *zap.SugaredLogger, r *http.Request) []byte {
	header := fmt.Sprintf("GET %s HTTP/1.1\r\n", r.URL.String())
	for name, values := range r.Header {
		header = header + fmt.Sprintf("%s:%s\r\n", name, strings.Join(values, ","))
	}
	header = header + fmt.Sprintf("\r\n")

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return nil
	}
	return append([]byte(header), body...)
}
