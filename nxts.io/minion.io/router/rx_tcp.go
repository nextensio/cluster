/*
 * rx_tcp.go: Rx Packet Processor 
 * 
 * Author: Davi Gupta (davigupta@gmail.com), Jun 2019
 */

package router

import (
    "io"
    "net"
    "bufio"
    "strings"
    "strconv"
    "net/http"
    "go.uber.org/zap"
    "minion.io/common"
    "minion.io/auth"
)

type TcpSeConn struct {
    track *TcpRxTracker
    conn net.Conn
}

func httpSendOk(handle *TcpSeConn, s *zap.SugaredLogger) {
    resp := []byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n")
    _, e := handle.conn.Write(resp)
    if e != nil {
        s.Errorw("rx_tcp:", "err", e)
    }
    s.Debug("rx_tcp: http ok")
}

func httpForLeft(pak []byte, s *zap.SugaredLogger) {
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
        if auth.AccessOk(left.clitype, left.uuid, usr, s) == false {
            s.Debug("rx_tcp: access denied, packet drop")
        }
        left.send <- pak
    } else {
        s.Debug("rx_tcp: packet drop")
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
         len, e:= c.conn.Read(buf)
         if e == io.EOF {
             s.Info("rx_tcp: conn EOF received")
             break
         }
         if e != nil {
             s.Errorw("rx_tcp:", "err", e)
         } else {
             pLen += Execute(state, buf, len, s)
             if IsBodyComplete(state) == true {
                 httpSendOk(c, s)
                 pak := append(GetHeaders(state), GetBody(state)...)
                 httpForLeft(pak, s)
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
