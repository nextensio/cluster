package shared

// Constants used by this program
const (
	SelfDest   = 1
	LocalDest  = 2
	RemoteDest = 3
)

// Structure for storing various parameters for this program
type Params struct {
	Iport     int
	Oport     int
	Node      string
	Pod       string
	Namespace string
	PodIp     string
	Id        string
	MongoUri  string
	Host      string
}

// Structure for storing forwarding result
type Fwd struct {
	DestType int
	Pod      string
	Id       string
	Dest     string
}
