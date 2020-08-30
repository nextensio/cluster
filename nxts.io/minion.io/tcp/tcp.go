/*
 * tcp.go: Handle incoming tcp request. Keep the TCP connection persistent for a 
 *         a configured time
 * 
 * Davi Gupta, davigupta@gmail.com, Jun 2019
 */

package tcp

import (
    "io"
    "net"
    "time"
    "go.uber.org/zap"
    "minion.io/common"
)

type tconn struct {
     conn net.Conn
     alive bool
     last time.Time
     pos int
}

var pend []tconn
var ticker *time.Ticker

func TcpReaper(sugar *zap.SugaredLogger) {
    for range ticker.C {
        sugar.Debugf("Periodic : tick")
    }
}

func pendRemove(s []tconn, i int) []tconn {
    s[i] = s[len(s) - 1]
    return s[:len(s) - 1]
}

func TcpStartPeriod(sugar *zap.SugaredLogger) {
    ticker = time.NewTicker(common.Period * time.Millisecond)
    go TcpReaper(sugar)
}

func TcpStopPeriod(sugar *zap.SugaredLogger) {
    ticker.Stop()
}

type callback func(conn net.Conn, buf []byte, sugar *zap.SugaredLogger)

func TcpServer(fp callback, sugar *zap.SugaredLogger) (e error) {
    // Listen for incoming connections
    l, e := net.Listen("tcp", common.MyInfo.ListenIp + ":" + string(common.MyInfo.Iport))
    if e != nil {
        sugar.Errorw("TCP Listen", "error:", e)
        return e
    }

    // Close the listener when the application closes
    defer l.Close()
    sugar.Infow("TCP", "ListenIP:",  common.MyInfo.ListenIp, "Port:", common.MyInfo.Iport)
    for {
        // Listen for an incoming connection
        conn, e := l.Accept()
        if e != nil {
            sugar.Errorw("TCP Accept", "error:", e)
            return e
        } 

        // Handle connections in a new goroutine
        pend = append(pend, tconn{conn, true, time.Now(), 0})
        t := pend[len(pend) - 1]
        t.pos  = len(pend) - 1
        go handleRequest(t, fp, sugar)
    }
}

// Handle incoming requests
func handleRequest(handle tconn, fp callback, sugar *zap.SugaredLogger) {
    // Make a buffer to hold incoming data
    buf := make([]byte, common.TcpBuffSize)
    for {
        if handle.alive != true {
            break
        }
        _, e := handle.conn.Read(buf)
        if e == io.EOF {
            sugar.Info("TCP conn EOF received")
            break
        }
        if e != nil {
            sugar.Errorw("TCP conn", "error:", e)
        } else {
            handle.last = time.Now()
            fp(handle.conn, buf, sugar)
        }
    }
    sugar.Info("TCP conn closed")
    handle.conn.Close()
    pend = pendRemove(pend, handle.pos)
}
