/*
 * tx_tcp.go: Tx Packet Processor
 * 
 * Author: Davi Gupta (davigupta@gmail.com), Sep 2020
 */

package router

import (
    "net"
    "time"
    "strings"
    "strconv"
    "go.uber.org/zap"
    "minion.io/common"
)

type TcpClConn struct {
    conn net.Conn
    last time.Time
    send chan []byte
    name string
}

/*
 * txHandler:
 *     Tx Packet processing
 *     Handle packets going to another POD (inter or intra nextensio
 *     cluster)
 */
func (c *TcpClConn) txHandler(t *Tracker, s *zap.SugaredLogger) {
    ticker := time.NewTicker(common.IdlePeriod * time.Second)
    defer func() {
        ticker.Stop()
        c.conn.Close()
        t.close <- c
    }()

    for {
        select {
        case <- ticker.C:
            s.Debug("tx_tcp: check for idle activity")
            // if c.last > IDLE time, then close the connection
        case msg, ok := <- c.send:
            if !ok {
                return
            }
            s.Debug("tx_tcp: got the packet to sent")
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
func TcpClient(t *Tracker, name string, s *zap.SugaredLogger) (*TcpClConn, error) {
    portStr := strconv.Itoa(common.MyInfo.Iport)
    servAddr := strings.Join([]string{name, portStr}, ":")
    conn, e := net.Dial("tcp", servAddr)
    if e != nil {
        s.Debugw("tx_tcp:", "err", e)
        return nil, e
    }
    v := TcpClConn{conn: conn, last: time.Now(), 
                   send: make(chan []byte, common.MaxQueueSize), name: name}
    s.Debugf("tx_tcp: dial tcp connection to %v", servAddr)
    go v.txHandler(t, s)

    return &v, e
}

func TcpTxInit() {
} 
