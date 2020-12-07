/*
 * http.go: Handle incoming http request
 *
 * Author: Davi Gupta (davigupta@gmail.com), Jun 2019
 */

package router

import (
	"bufio"
	"bytes"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	//"io/ioutil"
	"minion.io/aaa"
	"minion.io/common"
	"minion.io/consul"
	"minion.io/stats"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  common.MaxMessageSize,
	WriteBufferSize: common.MaxMessageSize,
}

var (
	space = []byte{' '}
)

type WsClient struct {
	track    *Tracker
	conn     *websocket.Conn
	send     chan common.Queue
	codec    string
	name     [common.MaxService]string
	num      int
	clitype  string
	uuid     string
	name_reg [common.MaxService]bool
	counter  int
}

func (c *WsClient) txHandler(s *zap.SugaredLogger) {
	ticker := time.NewTicker(common.PingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case <-ticker.C:
			s.Debug("http: send ping message")
			c.conn.SetWriteDeadline(time.Now().Add(common.WriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				s.Errorf("http: ping message error - %v", err)
				return
			}
		case msg, ok := <-c.send:
			if !ok {
				break
			}
			c.conn.SetWriteDeadline(time.Now().Add(common.WriteWait))
			err := c.conn.WriteMessage(websocket.BinaryMessage, msg.Pak)
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway) {
					s.Errorf("http: unexpected ws closure during write to %v %v - %v", c.clitype, c.uuid, err)
					return
				}
				s.Errorf("http: txHandler write error to %s %s - error=%v", c.clitype, c.uuid, err)
			}
		}
	}
}

func isIpv4Net(host string) bool {
	return net.ParseIP(host) != nil
}

func (c *WsClient) rxHandler(s *zap.SugaredLogger) {
	var fwd common.Fwd

	defer func() {
		c.track.del <- c
		c.track.unregister <- c
		c.conn.Close()
		aaa.UsrLeave(c.clitype, c.uuid, s)
	}()

	c.conn.SetReadLimit(common.WsReadLimit)
	c.conn.SetReadDeadline(time.Now().Add(common.PongWait))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(common.PongWait)); return nil })

	// Read client info and send welcome
	messageType, p, e := c.conn.ReadMessage()
	if e != nil {
		s.Errorf("http rxHandler: %v", e)
		return
	}
	words := bytes.Split(p, space)
	if bytes.Equal(words[0], []byte("NCTR")) {
		c.clitype = "connector"
	} else {
		c.clitype = "agent"
	}
	s.Debugf("http type %v received from %s", messageType, c.clitype)
	if bytes.Equal(words[0], []byte("NCTR")) || bytes.Equal(words[0], []byte("NAGT")) {
		e = c.conn.WriteMessage(messageType, bytes.Join([][]byte{[]byte("Hello"), words[0]}, space))
		if e != nil {
			s.Errorf("http hello send error to %s: %v", c.clitype, e)
			return
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
	c.track.add <- c
	aaa.UsrJoin(c.clitype, c.uuid, s)

	var drop bool
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
		// forward the packet
		// get the destination to foward to
		reader := bufio.NewReader(strings.NewReader(string(p)))
		r, e := http.ReadRequest(reader)
		if e != nil {
			s.Errorf("http: message header/body parsing error - %v", e)
			continue
		}
		dest := r.Header.Get("x-nextensio-for")
		destinfo := strings.Split(dest, ":")
		host := destinfo[0]
		savedhost := host
		drop = false
		if isIpv4Net(host) {
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
			// do consul lookup
			var err error
			fwd, err = consul.ConsulDnsLookup(consul_key, s)
			if err != nil {
				s.Errorf("http: Consul lookup error with key %s - %v", consul_key, err)
				stats.PakDrop(p, "DNSLookupFailed", s)
				continue
			}
		}
		usrattr, attrok := aaa.GetUsrAttr(c.clitype, c.uuid, s)
		nhdrs := len(r.Header)
		// Split up the http headers. Get \r\n (blank line) plus body into last split.
		z := bytes.SplitN(p, []byte("\r\n"), nhdrs+1)
		p = z[0]
		hostfound := false
		forfound := false
		for i := 1; i < nhdrs; i = i + 1 {
			if (hostfound == false) && (fwd.DestType == common.RemoteDest) {
				if bytes.Contains(z[i], []byte("host:")) || bytes.Contains(z[i], []byte("Host:")) {
					z[i] = []byte("Host:" + " " + fwd.Dest)
					hostfound = true
				}
			}
			if forfound == false {
				if bytes.Contains(z[i], []byte("sio-for:")) {
					z[i] = []byte("x-nextensio-for:" + " " + savedhost)
					forfound = true
				}
			}
			p = bytes.Join([][]byte{p, z[i]}, []byte("\r\n"))
		}
		if attrok {
			// Add user attributes header in apod to cpod direction only
			attrb := []byte("x-nextensio-attr: " + usrattr)
			p = bytes.Join([][]byte{p, attrb, z[nhdrs]}, []byte("\r\n"))
		} else {
			p = bytes.Join([][]byte{p, z[nhdrs]}, []byte("\r\n"))
		}

		if fwd.DestType == common.SelfDest {
			left := LookupLeftService(fwd.Dest)
			if left != nil {
				item := common.Queue{Id: c.counter, Pak: p}
				left.send <- item
				s.Debugf("http: pak %v for %v put into Q after LookupLeftService", c.counter, fwd.Dest)
			} else {
				drop = true
				stats.PakDrop(p, "LookupFailedLeft", s)
			}
		} else {
			// open a TCP connection if not opened
			right := LookupRightDest(fwd.Dest)
			if right == nil {
				c.track.connect <- fwd.Dest
				right = <-c.track.conn
				s.Debugf("http: pak %v for %v needs TCP conn opened after LookupRightDest", c.counter, fwd.Dest)
			}
			if right != nil {
				item := common.Queue{Id: c.counter, Pak: p}
				right.send <- item
				s.Debugf("http: pak %v for %v put into Q after LookupRightDest", c.counter, fwd.Dest)
			} else {
				drop = true
				stats.PakDrop(p, "LookupFailedRight", s)
			}
		}
		if drop == false {
			s.Debugf("http: pak rxcount %v", c.counter)
			c.counter++
		}
	}
}

func wsEndpoint(t *Tracker, w http.ResponseWriter, r *http.Request,
	s *zap.SugaredLogger) {
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
	// add the connection for the bookeeping
	client := &WsClient{track: t, conn: ws,
		send:  make(chan common.Queue, common.MaxQueueSize),
		codec: codec, clitype: "connector",
		uuid: uuid}
	client.track.register <- client
	s.Infof("http access for uuid %s allowed", uuid)

	go client.txHandler(s)
	go client.rxHandler(s)
}

// Register for websocket handler
func setupRoutes(t *Tracker, s *zap.SugaredLogger) {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		wsEndpoint(t, w, r, s)
	})
}

// Start http server
func HttpStart(s *zap.SugaredLogger) error {
	track := NewTracker()
	go track.run(s)
	setupRoutes(track, s)
	portStr := strconv.Itoa(common.MyInfo.Oport)
	addr := strings.Join([]string{common.MyInfo.ListenIp, portStr}, ":")
	e := http.ListenAndServe(addr, nil)
	if e != nil {
		s.Errorw("http server start failure", "err", e)
	}
	s.Debugf("http: Server set up to listen at %s", string(addr))

	return e
}
