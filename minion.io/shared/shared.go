/*
 * shared.go: variables, constant, structures shared across packages
 *
 * Author: Davi Gupta (davigupta@gmail.com), Jun 2019
 */

package shared

// Constants used by this program
const (
	SelfDest   = 1
	LocalDest  = 2
	RemoteDest = 3
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

// Structure for storing forwarding result
type Fwd struct {
	DestType int
	Pod      string
	Id       string
	Dest     string
}
