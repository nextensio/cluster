/*
 * http.go: Handle incoming http request
 *
 * Author: Davi Gupta (davigupta@gmail.com), Jun 2019
 */

package router

import (
	"bufio"
	"bytes"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"net"
	"net/http"
	"strconv"
	"strings"

	"minion.io/aaa"
	"minion.io/common"
	"minion.io/consul"
	"minion.io/stats"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  common.MaxMessageSize,
	WriteBufferSize: common.MaxMessageSize,
}

var (
	space = []byte{' '}
)

type WsClient struct {
	connid   string
	conn     *websocket.Conn
	codec    string
	name     [common.MaxService]string
	num      int
	clitype  string
	uuid     string
	name_reg [common.MaxService]bool
	counter  int
}

// Periodically send a ping which the other end will pong, this keeps the
// websocket alive or else it tears down on inactivity after one minute
func wsPing(s *zap.SugaredLogger, c *WsClient) {
	for {
		s.Debug("websocket: send ping message")
		if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
			s.Errorf("http: ping message error - %v", err)
			return
		}
		time.Sleep(common.PingPeriod)
	}
}

// On the websocket, the agent connects and first sends a hello, only after
// that the data starts flowing
func wsAgentHello(s *zap.SugaredLogger, c *WsClient) error {
	messageType, p, e := c.conn.ReadMessage()
	if e != nil {
		s.Errorf("http rxHandler: %v", e)
		return e
	}
	words := bytes.Split(p, space)
	if bytes.Equal(words[0], []byte("NCTR")) {
		c.clitype = "connector"
	} else {
		c.clitype = "agent"
	}
	if bytes.Equal(words[0], []byte("NCTR")) || bytes.Equal(words[0], []byte("NAGT")) {
		e = c.conn.WriteMessage(messageType, bytes.Join([][]byte{[]byte("Hello"), words[0]}, space))
		if e != nil {
			s.Errorf("http hello send error to %s: %v", c.clitype, e)
			return e
		}
	}
	// Register services to consul
	j := 0
	for i := 1; i < len(words); i++ {
		// The agent is currently inserting 2 spaces between NAGT and the agent id
		// This was resulting in registering a bogus Consul service with name " " (one space)
		nstr := string(bytes.TrimLeft(words[i], " "))
		if nstr == "" {
			continue
		}
		c.name[j] = nstr
		j = j + 1
	}
	c.num = j
	s.Debugf("http: received %v services from %s", c.num, c.clitype)
	// add loopback service
	c.name[c.num] = "127-0-0-1"
	c.num += 1
	addService(c, s)
	aaa.UsrJoin(c.clitype, c.uuid, s)
	return nil
}

// TODO: Implement this to send a flow termination message back on the channel,
// so that the other end can take necessary action to terminate the flow. Also,
// this needs to be done only for "proxied" flows - ie TCP terminated flows. We
// can also be carrying raw IP packets here, that does not need  flow terminate.
// The parameter to this API is the list of http headers (nextensio headers and
// other general http headers) - the nextensio headers in there should be able to
// give us flow identification and information on whether the flow is raw IP or
// proxied etc..
func sendWsFlowTerm(s *zap.SugaredLogger, c *WsClient, headers map[string][]string, reason string, data []byte) {
	stats.PakDrop(data, reason, s)
	s.Errorf("Packet dropped because of %s", reason)
}

func (c *WsClient) rxHandler(s *zap.SugaredLogger) {
	defer func() {
		delService(c, s)
		c.conn.Close()
		aaa.UsrLeave(c.clitype, c.uuid, s)
	}()

	c.conn.SetReadLimit(common.WsReadLimit)
	if wsAgentHello(s, c) != nil {
		return
	}
	go wsPing(s, c)

	// Read packets and forward
	for {
		messageType, p, e := c.conn.ReadMessage()
		if e != nil {
			if websocket.IsUnexpectedCloseError(e, websocket.CloseGoingAway) {
				s.Errorf("http: ws unexpected closure during read - %v", e)
				break
			}
			s.Errorf("http: ws read message error - %v", e)
			break
		}
		reader := bufio.NewReader(strings.NewReader(string(p)))
		r, e := http.ReadRequest(reader)
		if e != nil {
			// Well, not sure what kind of flow termination we can send here
			// if we cant even parse the data to figure out what flow it is
			s.Errorf("http: message header/body parsing error - %v", e)
			continue
		}
		dest := r.Header.Get("x-nextensio-for")
		destinfo := strings.Split(dest, ":")
		host := destinfo[0]
		savedhost := host

		var fwd common.Fwd
		if net.ParseIP(host) != nil {
			fwd.Dest = host
			fwd.DestType = common.LocalDest
			s.Debugf("http type=%v, dest=%s, host:%s (LocalDest)", messageType, dest, host)
		} else {
			s.Debugf("http type=%v, dest=%s, host:%s", messageType, dest, host)
			var consul_key string
			tag := aaa.RouteLookup(c.clitype, c.uuid, host, s)
			host = strings.ReplaceAll(host, ".", "-")
			if tag == "" {
				consul_key = strings.Join([]string{host, common.MyInfo.Namespace}, "-")
			} else {
				consul_key = strings.Join([]string{tag, host, common.MyInfo.Namespace}, "-")
				savedhost = tag + "." + savedhost
			}
			var err error
			fwd, err = consul.ConsulDnsLookup(consul_key, s)
			if err != nil {
				sendWsFlowTerm(s, c, r.Header, "DNSLookupFailed", p)
				continue
			}
		}

		r.Header.Del("host")
		r.Header.Add("host", fwd.Dest)
		r.Header.Del("x-nextensio-for")
		r.Header.Add("x-nextensio-for", savedhost)
		usrattr, attrok := aaa.GetUsrAttr(c.clitype, c.uuid, s)
		if attrok {
			// Add user attributes header in apod to cpod direction only
			r.Header.Add("x-nextensio-attr", usrattr)
		}

		if fwd.DestType == common.SelfDest {
			// The destination agent/connector is in this same pod
			left := LookupLeftService(fwd.Dest)
			if left != nil {
				data := common.HttpToBytes(s, r)
				err := left.conn.WriteMessage(websocket.BinaryMessage, data)
				if err != nil {
					sendWsFlowTerm(s, c, r.Header, "WebsocketWriteFail", p)
				}
				s.Debugf("http: pak %v for %v put into Q after LookupLeftService", c.counter, fwd.Dest)
			} else {
				sendWsFlowTerm(s, c, r.Header, "LookupFailedLeft", p)
			}
		} else {
			// open an HTTP connection and send the request to either a pod in
			// the same cluster or a pod in the remote cluster
			err := sendRight(s, c.connid, fwd.Dest, r)
			if err != nil {
				sendWsFlowTerm(s, c, r.Header, "Websocket2HttpFail", p)
			}
		}
	}
}

func wsEndpoint(w http.ResponseWriter, r *http.Request, s *zap.SugaredLogger) {
	//TODO : Fix handling of Origin
	upgrader.CheckOrigin = func(r *http.Request) bool {
		if r.Header.Get("Origin") != "http://"+r.Host {
			return true
		} else {
			return true
		}
	}

	codec := r.Header.Get("x-nextensio-codec")
	//TODO: Check for supported codec

	ws, e := upgrader.Upgrade(w, r, nil)
	if e != nil {
		s.Errorw("http", "err", e)
		return
	}

	uuid := r.Header.Get("x-nextensio-uuid")
	s.Debugf("http: Client connected for uuid=%s with codec=%s", uuid, codec)
	allowed := aaa.UsrAllowed(uuid, s)
	if allowed == false {
		s.Infof("http access for uuid %s denied", uuid)
		ws.Close()
		return
	}

	// TODO: The "connid" field indicates a name for the connection from the agent.
	// The agent can open multiple connections to the cluster, the plan is that each
	// connection will have a random number to uniquely identify it, so the combination
	// of the uuid and the random number will be the connid .. The random number will
	// come in as a nextensio header soon, WIP
	client := &WsClient{conn: ws,
		codec: codec, clitype: "connector",
		uuid:   uuid,
		connid: uuid}
	s.Infof("http access for uuid %s allowed", uuid)

	client.rxHandler(s)
}

// Start http server to deal with http requests from outside this cluster
func HttpExternalStart(s *zap.SugaredLogger) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		wsEndpoint(w, r, s)
	})
	portStr := strconv.Itoa(common.MyInfo.Oport)
	addr := common.MyInfo.ListenIp + ":" + portStr
	server := http.Server{Addr: addr, Handler: mux}
	e := server.ListenAndServe()
	if e != nil {
		s.Errorw("http server start failure", "err", e)
	}
	s.Debugf("http: Server set up to listen at %s", string(addr))

	return e
}
