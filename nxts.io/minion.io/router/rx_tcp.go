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
		rlen, e := c.conn.Read(buf[pLen:])
		if e == io.EOF {
			s.Info("rx_tcp: conn EOF received in handleHttpRequests")
			break
		}
		if e != nil {
			s.Errorf("rx_tcp: err while reading connection in handleHttpRequests - %v", e)
			break
		} else {
			uLen := Execute(state, buf, pLen+rlen, s)
			if IsBodyComplete(state) == true {
				s.Debugf("rx_tcp: got pak %v\n", c.counter)
				httpSendOk(c, s)
				pak := append(GetHeaders(state), GetBody(state)...)
				httpForLeft(c, pak, s)
				c.counter++
				if uLen < (pLen + rlen) {
					// Didn't use up all data in buf. Move unused data to start of buf.
					// Set pLen so next read gets data after this unused data.
					diff := pLen + rlen - uLen
					for i := 0; i < diff; i++ {
						buf[i] = buf[uLen+i]
					}
					pLen = diff
				} else {
					// All data used up. Reset pLen to 0
					pLen = 0
				}
			} else if IsHeaderComplete(state) == true {
				s.Debugf("rx_tcp: rcvd Hdr (%v), not body, clen=%v, plen=%v, cursor=%v",
					len(state.header), state.clen, state.plen, state.cursor)
			} else {
				s.Debugf("rx_tcp: rcvd neither hdr nor body, rlen=%v, pLen=%v, uLen=%v, cursor=%v",
					rlen, pLen, uLen, state.cursor)
				// Discard received data and start from scratch if headers don't end within
				// 1K bytes. Seems like some ghost non-http frame gets in once in a while.
				// TODO: Need more debugging to figure out why this is happening.
				if state.cursor > 1024 {
					s.Errorf("rx_tcp: discarded frame w/o http headers end after %v bytes",
						state.cursor)
					stateInit(state)
					pLen = 0
				}
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
