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
	"io/ioutil"
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
	var rewrite bool

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
		s.Errorw("http", "err", e)
	}
	s.Debugw("http", "type", messageType)
	s.Debugw("http", "body", string(p))
	words := bytes.Split(p, space)
	if bytes.Equal(words[0], []byte("NCTR")) {
		c.clitype = "connector"
	} else {
		c.clitype = "agent"
	}
	s.Debugw("http", "clitype", c.clitype)
	if bytes.Equal(words[0], []byte("NCTR")) || bytes.Equal(words[0], []byte("NAGT")) {
		e = c.conn.WriteMessage(messageType, bytes.Join([][]byte{[]byte("Hello"), words[0]}, space))
		if e != nil {
			s.Errorw("http", "err", e)
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
		s.Debugw("http", "type", messageType)
		// forward the packet
		// get the destination to foward to
		reader := bufio.NewReader(strings.NewReader(string(p)))
		r, e := http.ReadRequest(reader)
		if e != nil {
			s.Errorw("http", "err", e)
		}
		s.Debugw("http", "header", r.Header)
		body, _ := ioutil.ReadAll(r.Body)
		s.Debugw("http", "type", string(body))
		dest := r.Header.Get("x-nextensio-for")
		s.Debugw("http", "dest", dest)
		destinfo := strings.Split(dest, ":")
		host := destinfo[0]
		if isIpv4Net(host) {
			fwd.Dest = host
			fwd.DestType = common.LocalDest
		} else {
			host = strings.ReplaceAll(host, ".", "-")
			consul_key := strings.Join([]string{host, common.MyInfo.Namespace}, "-")
			s.Debugw("http", "key", consul_key)
			// do consul lookup
			fwd, _ = consul.ConsulDnsLookup(consul_key, s)
		}
		usr, ok := aaa.GetUsrAttr(c.clitype, c.uuid, s)
		rewrite = false
		if ok {
			attr := "x-nextensio-attr: " + usr
			attrb := []byte(attr)
			z := bytes.SplitN(p, []byte("\r\n"), 3)
			newhost := z[1]
			if fwd.DestType == common.RemoteDest {
				rewrite = true
				newhost = bytes.Join([][]byte{[]byte("Host:"), []byte(fwd.Dest)}, []byte(" "))
			}
			p = bytes.Join([][]byte{z[0], newhost, attrb, z[2]}, []byte("\r\n"))
		}
		drop = false
		if fwd.DestType == common.SelfDest {
			left := LookupLeftService(fwd.Dest)
			if left != nil {
				item := common.Queue{Id: c.counter, Pak: p}
				left.send <- item
			} else {
				drop = true
				stats.PakDrop(p, "lookup left failure", s)
			}
		} else {
			if fwd.DestType == common.RemoteDest {
				// rewrite HOST part in GET
				if rewrite == false {
					z := bytes.SplitN(p, []byte("\r\n"), 3)
					rewrite = true
					newhost := bytes.Join([][]byte{[]byte("Host:"), []byte(fwd.Dest)}, []byte(" "))
					p = bytes.Join([][]byte{z[0], newhost, z[2]}, []byte("\r\n"))
				}
			}

			// open a TCP connection if not opened
			right := LookupRightDest(fwd.Dest)
			if right == nil {
				c.track.connect <- fwd.Dest
				right = <-c.track.conn
			}
			if right != nil {
				item := common.Queue{Id: c.counter, Pak: p}
				right.send <- item
			} else {
				drop = true
				stats.PakDrop(p, "lookup right failure", s)
			}
		}
		if drop == false {
			s.Debugw("http:", "pak", c.counter)
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
	s.Debugw("http", "codec", codec)
	//TODO: Check for supported codec

	ws, e := upgrader.Upgrade(w, r, nil)
	if e != nil {
		s.Errorw("http", "err", e)
		return
	}

	s.Debug("http: Client connected")

	uuid := r.Header.Get("x-nextensio-uuid")
	s.Debugw("http", "uuid", uuid)
	allowed := aaa.UsrAllowed(uuid, s)
	if allowed == false {
		s.Infow("http", "uuid", uuid, "access", allowed)
		ws.Close()
		return
	}
	// add the connection for the bookeeping
	client := &WsClient{track: t, conn: ws,
		send:  make(chan common.Queue, common.MaxQueueSize),
		codec: codec, clitype: "connector",
		uuid: uuid}
	client.track.register <- client

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
	s.Debug("http", string(addr))
	e := http.ListenAndServe(addr, nil)
	if e != nil {
		s.Errorw("http", "err", e)
	}

	return e
}
