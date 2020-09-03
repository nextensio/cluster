/*
 * tx_tcp.go: Tx Packet Processor
 * 
 * Davi Gupta, davigupta@gmail.com, Sep 2020
 */

package router

import (
    "net"
    "time"
    "strings"
    "go.uber.org/zap"
    "minion.io/common"
)

type TcpClConn struct {
    conn net.Conn
    last time.Time
    send chan []byte
}

/*
 * txHandler:
 *     Tx Packet processing
 *     Handle packets going to another POD (inter or intra nextensio
 *     cluster)
 */
func (c *TcpClConn) txHandler(s *zap.SugaredLogger) {
    ticker := time.NewTicker(common.IdlePeriod)
    defer func() {
        ticker.Stop()
        c.conn.Close()
    }()

    for {
        select {
        case <- ticker.C:
            s.Debug("tcp: check for idle activity")
            // if c.last > IDLE time, then close the connection
        case msg, ok := <- c.send:
            if !ok {
                return
            }
            c.conn.Write(msg)
            n := len(c.send)
            for i := 0; i < n; i++ {
                c.conn.Write(<-c.send)
            }
            c.last = time.Now()
        }
    }
}

/*
 * TcpClient:
 *     create connection to another POD (inter or intra nextensio
 *     cluster)
 */
func TcpClient(name string, s *zap.SugaredLogger) (*TcpClConn, error) {
    servAddr := strings.Join([]string{name, "80"}, ":")
    conn, e := net.Dial("tcp", servAddr)
    if e != nil {
        s.Debugw("tcp:", "err", e)
        return nil, e
    }
    v := TcpClConn{conn: conn, last: time.Now(), 
                   send: make(chan []byte, common.MaxQueueSize)}
    go v.txHandler(s)

    return &v, e
}

func TcpTxInit() {
} 
