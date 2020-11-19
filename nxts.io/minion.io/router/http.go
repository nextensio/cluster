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
				return
			}
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(common.WriteWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			w, err := c.conn.NextWriter(websocket.BinaryMessage)
			if err != nil {
				break
			}
			w.Write(msg.Pak)
			if err := w.Close(); err != nil {
				break
			}

			n := len(c.send)
			for i := 0; i < n; i++ {
				msg, _ = <-c.send
				w, err := c.conn.NextWriter(websocket.BinaryMessage)
				if err != nil {
					break
				}
				w.Write(msg.Pak)
				if err := w.Close(); err != nil {
					break
				}
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

	c.conn.SetReadLimit(common.MaxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(common.PongWait))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(common.PongWait)); return nil })

	// Read client info and send welcome
	messageType, p, e := c.conn.ReadMessage()
	if e != nil {
		s.Errorf("http rxHandler: %v", e)
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
			// Do we continue ?
		}
	}
	// Register services to consul
	for i := 1; i < len(words); i++ {
		c.name[i-1] = string(words[i])
	}
	c.num = len(words) - 1
	// add loopback service
	c.name[c.num] = "127-0-0-1"
	c.num += 1
	c.track.add <- c
	aaa.UsrJoin(c.clitype, c.uuid, s)

	var drop bool
	// Read the packet and forward
	for {
		messageType, p, e = c.conn.ReadMessage()
		if e != nil {
			if websocket.IsUnexpectedCloseError(e, websocket.CloseGoingAway) {
				s.Errorw("http", "err", e)
			}
			s.Errorw("http", "err", e)
			break
		}
		// forward the packet
		// get the destination to foward to
		reader := bufio.NewReader(strings.NewReader(string(p)))
		r, e := http.ReadRequest(reader)
		if e != nil {
			s.Errorw("http", "err", e)
			// Do we continue ?
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
				s.Debugf("http: Consul lookup error with key %s - %v", consul_key, err)
			} else {
				s.Debugf("http: Consul lookup with key %s gave %v", consul_key, fwd)
			}
		}
		usrattr, attrok := aaa.GetUsrAttr(c.clitype, c.uuid, s)
		nhdrs := len(r.Header)
		s.Debugf("http: pak from %s has %v headers", c.clitype, nhdrs)
		z := bytes.SplitN(p, []byte("\r\n"), nhdrs+1)
		p = z[0]
		// Try to improve this later
		for i := 1; i < nhdrs; i = i + 1 {
			if bytes.Contains(z[i], []byte("host:")) {
				z[i] = bytes.Join([][]byte{[]byte("Host:"), []byte(fwd.Dest)}, []byte(" "))
			} else if bytes.Contains(z[i], []byte("Host:")) {
				z[i] = bytes.Join([][]byte{[]byte("Host:"), []byte(fwd.Dest)}, []byte(" "))
			} else if bytes.Contains(z[i], []byte("x-nextensio-for:")) {
				z[i] = bytes.Join([][]byte{[]byte("X-Nextensio-For:"), []byte(savedhost)}, []byte(" "))
			} else if bytes.Contains(z[i], []byte("X-Nextensio-For:")) {
				z[i] = bytes.Join([][]byte{[]byte("X-Nextensio-For:"), []byte(savedhost)}, []byte(" "))
			}
			p = bytes.Join([][]byte{p, z[i]}, []byte("\r\n"))
		}
		if attrok {
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
			s.Debugw("http:", "pak rxcount", c.counter)
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
