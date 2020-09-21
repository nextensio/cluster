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
		s.Errorw("rx_tcp:", "err", e)
	}
	s.Debugf("rx_tcp: http ok %v\n", handle.counter)
}

func httpForLeft(handle *TcpSeConn, pak []byte, s *zap.SugaredLogger) {
	reader := bufio.NewReader(strings.NewReader(string(pak)))
	r, e := http.ReadRequest(reader)
	if e != nil {
		s.Errorw("rx_tcp:", "err", e)
	}
	dest := r.Header.Get("x-nextensio-for")
	destinfo := strings.Split(dest, ":")
	host := destinfo[0]
	host = strings.ReplaceAll(host, ".", "-")
	left := LookupLeftService(host)
	if left != nil {
		// Check whether it is allowed
		usr := r.Header.Get("x-nextensio-attr")
		if aaa.AccessOk(left.clitype, left.uuid, usr, s) == false {
			stats.PakDrop(pak, "access denied", s)
		}
		item := common.Queue{Id: handle.counter, Pak: pak}
		left.send <- item
	} else {
		stats.PakDrop(pak, "lookup left failure", s)
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
		len, e := c.conn.Read(buf)
		if e == io.EOF {
			s.Info("rx_tcp: conn EOF received")
			break
		}
		if e != nil {
			s.Errorw("rx_tcp:", "err", e)
		} else {
			pLen += Execute(state, buf, len, s)
			if IsBodyComplete(state) == true {
				s.Debugf("rx_tcp: got pak %v\n", c.counter)
				httpSendOk(c, s)
				pak := append(GetHeaders(state), GetBody(state)...)
				httpForLeft(c, pak, s)
				c.counter++
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
		s.Errorw("rx_tcp:", "err", e)
		return e
	}

	// Close the listener when the application closes
	defer l.Close()
	s.Infow("rx_tcp:", "addr", addr)
	for {
		// Listen for an incoming connection
		conn, e := l.Accept()
		if e != nil {
			s.Errorw("rx_tcp:", "err", e)
			return e
		}

		// Handle connections in a new goroutine
		client := &TcpSeConn{track: t, conn: conn}
		client.track.register <- client

		go client.handleHttpRequest(s)
	}
}

func TcpRxStart(s *zap.SugaredLogger) {
	track := NewTcpRxTracker()
	go track.run(s)
	go TcpServer(track, s)
}
