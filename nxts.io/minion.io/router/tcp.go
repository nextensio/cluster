/*
 * tcp.go: Handle incoming tcp request. Keep the TCP connection persistent for a 
 *         a configured time
 * 
 * Davi Gupta, davigupta@gmail.com, Jun 2019
 */

package router

import (
    "io"
    "net"
    "time"
    "bufio"
    "strings"
    "strconv"
    "net/http"
    "go.uber.org/zap"
    "minion.io/common"
)

type sconn struct {
     conn net.Conn
     pos int
}

type cconn struct {
     conn net.Conn
     pos int
     live bool
     last time.Time
}

var tcpSerConn []*sconn
var tcpCliConn []*cconn
var ticker *time.Ticker

func TcpReaper(sugar *zap.SugaredLogger) {
    for range ticker.C {
        sugar.Debug("tcp: tick")
    }
}

func sconnRemove(s []*sconn, i int) {
    s[i] = s[len(s) - 1]
    s = s[:len(s) - 1]
}

func TcpStartPeriod(s *zap.SugaredLogger) {
    ticker = time.NewTicker(common.Period * time.Millisecond)
    go TcpReaper(s)
}

func TcpStopPeriod(s *zap.SugaredLogger) {
    ticker.Stop()
}

func httpSendOk(handle *sconn, s *zap.SugaredLogger) {
    resp := []byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n")
    _, e := handle.conn.Write(resp)
    if e != nil {
        s.Errorw("tcp:", "err", e)
    }
    s.Debug("tcp: http ok")
}

func httpForLeft(pak []byte, s *zap.SugaredLogger) {
    reader := bufio.NewReader(strings.NewReader(string(pak)))
    r, e := http.ReadRequest(reader)
    if e != nil {
        s.Errorw("tcp:", "err", e)
    }
    dest := r.Header.Get("x-nextensio-for")
    destinfo := strings.Split(dest, ":")
    host := destinfo[0]
    host = strings.ReplaceAll(host, ".", "-")
    left := ServiceLeft[host]
    if left != nil {
        left.send <- pak
    } else {
        s.Debug("tcp: packet drop")
    }
}

func handleHttpRequest(handle *sconn, s *zap.SugaredLogger) {
    // Make a buffer to hold incoming data
    var pLen int = 0
    buf := make([]byte, common.TcpBuffSize)
    state := InitCtx()
    for {
         len, e:= handle.conn.Read(buf)
         if e == io.EOF {
             s.Info("tcp: conn EOF received")
             break
         }
         if e != nil {
             s.Errorw("tcp:", "err", e)
         } else {
             pLen += Execute(state, buf, len, s)
             if IsBodyComplete(state) == true {
                 httpSendOk(handle, s)
                 pak := append(GetHeaders(state), GetBody(state)...)
                 httpForLeft(pak, s)
             }
         }
    }
}

func TcpServer(s *zap.SugaredLogger) error {
    // Listen for incoming connections
    portStr := strconv.Itoa(common.MyInfo.Oport)
    addr := strings.Join([]string{common.MyInfo.ListenIp, portStr}, ":")
    l, e := net.Listen("tcp", addr)
    if e != nil {
        s.Errorw("tcp:", "err", e)
        return e
    }   

    // Close the listener when the application closes
    defer l.Close()
    s.Infow("TCP", addr)
    for {
        // Listen for an incoming connection
        conn, e := l.Accept()
        if e != nil {
            s.Errorw("tcp:", "err", e)
            return e
        }

        // Handle connections in a new goroutine
        tcpSerConn = append(tcpSerConn, &sconn{conn, 0})
        t := tcpSerConn[len(tcpSerConn) - 1]
        t.pos  = len(tcpSerConn) - 1
        go handleHttpRequest(t, s)
    }

    return nil
}

func TcpClient(s *zap.SugaredLogger) error {
    return nil
}
