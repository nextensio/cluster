/*
 * rx_tcp.go: Rx Packet Processor
 *
 * Author: Davi Gupta (davigupta@gmail.com), Jun 2019
 */

package router

import (
	"bufio"
	"go.uber.org/zap"
	"io"
	"minion.io/aaa"
	"minion.io/common"
	"minion.io/stats"
	"net"
	"net/http"
	"strconv"
	"strings"
)

type TcpSeConn struct {
	track   *TcpRxTracker
	conn    net.Conn
	counter int
}

func httpSendOk(handle *TcpSeConn, s *zap.SugaredLogger) {
	resp := []byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
	_, e := UtilWrite(handle.conn, resp)
	if e != nil {
		s.Errorf("rx_tcp: SendOk err %v", e)
	}
	s.Debugf("rx_tcp: http ok %v\n", handle.counter)
}

func httpForLeft(handle *TcpSeConn, pak []byte, s *zap.SugaredLogger) {
	reader := bufio.NewReader(strings.NewReader(string(pak)))
	r, e := http.ReadRequest(reader)
	if e != nil {
		stats.PakDrop(pak, "ReaderFailure", s)
		s.Errorf("rx_tcp: httpForLeft ReadRequest err - %v", e)
		return
	}
	dest := r.Header.Get("x-nextensio-for")
	destinfo := strings.Split(dest, ":")
	host := destinfo[0]
	host = strings.ReplaceAll(host, ".", "-")
	left := LookupLeftService(host)
	if left != nil {
		s.Debugf("rx_tcp: LookupLeftService for host %s returned cltype=%s, uuid=%s", host, left.clitype, left.uuid)
		// Check whether it is allowed
		// TODO Do we need to check clitype ? Should not happen for return traffic
		usr := r.Header.Get("x-nextensio-attr")
		if aaa.AccessOk(left.clitype, left.uuid, usr, s) == false {
			stats.PakDrop(pak, "AccessDenied", s)
			return
		}
		item := common.Queue{Id: handle.counter, Pak: pak}
		left.send <- item
	} else {
		stats.PakDrop(pak, "LookupFailedLeft", s)
	}
}

func (c *TcpSeConn) handleHttpRequest(s *zap.SugaredLogger) {
	// Make a buffer to hold incoming data
	var pLen int = 0
	buf := make([]byte, common.TcpBuffSize)
	state := InitCtx()
	defer func() {
		c.track.unregister <- c
		c.conn.Close()
	}()
	for {
		rlen, e := c.conn.Read(buf)
		if e == io.EOF {
			s.Info("rx_tcp: conn EOF received in handleHttpRequests")
			break
		}
		if e != nil {
			s.Errorf("rx_tcp: err while reading connection in handleHttpRequests - %v", e)
		} else {
			pLen += Execute(state, buf, rlen, s)
			if IsBodyComplete(state) == true {
				s.Debugf("rx_tcp: got pak %v\n", c.counter)
				httpSendOk(c, s)
				pak := append(GetHeaders(state), GetBody(state)...)
				httpForLeft(c, pak, s)
				c.counter++
			} else if IsHeaderComplete(state) == true {
				s.Debugf("rx_tcp: rcvd Hdr (%v), not body, clen=%v, plen=%v, cursor=%v",
					len(state.header), state.clen, state.plen, state.cursor)
			} else {
				s.Debugf("rx_tcp: rcvd neither hdr nor body, pLen=%v, cursor=%v",
					pLen, state.cursor)
			}
		}
	}
}

func TcpServer(t *TcpRxTracker, s *zap.SugaredLogger) error {
	// Listen for incoming connections
	portStr := strconv.Itoa(common.MyInfo.Iport)
	addr := strings.Join([]string{common.MyInfo.ListenIp, portStr}, ":")
	l, e := net.Listen("tcp", addr)
	if e != nil {
		s.Errorf("rx_tcp: Server listen err - %v", e)
		return e
	}

	// Close the listener when the application closes
	defer l.Close()
	s.Infof("rx_tcp: TCP server set to listen on addr %s", addr)
	for {
		// Listen for an incoming connection
		conn, e := l.Accept()
		if e != nil {
			s.Errorf("rx_tcp: Server accept err - %v", e)
			continue
		}

		// Handle connections in a new goroutine
		client := &TcpSeConn{track: t, conn: conn}
		client.track.register <- client
		s.Debugf("rx_tcp: TCP server accepted incoming connection from %v", conn.RemoteAddr())

		go client.handleHttpRequest(s)
	}
}

func TcpRxStart(s *zap.SugaredLogger) {
	track := NewTcpRxTracker()
	go track.run(s)
	go TcpServer(track, s)
}
