/*
 * tx_tcp.go: Tx Packet Processor
 *
 * Author: Davi Gupta (davigupta@gmail.com), Sep 2020
 */

package router

import (
	"go.uber.org/zap"
	"minion.io/common"
	"net"
	"strconv"
	"strings"
	"time"
	"io"
)

type TcpClConn struct {
	conn net.Conn
	last time.Time
	send chan []byte
	name string
	counter int
}

const ACK_RESP = 256

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
		var tmp []byte = make([]byte, ACK_RESP)
		select {
		case <-ticker.C:
			s.Debug("tx_tcp: check for idle activity")
			// if c.last > IDLE time, then close the connection
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			s.Debugf("tx_tcp: packet sent %v\n", c.counter)
			c.conn.Write(msg)
			_, e := c.conn.Read(tmp)
			if e != nil {
				if e != io.EOF {
					s.Errorw("tx_tcp:", "err", e)
				}
			} else {
				s.Debugf("tx_tcp: got ack %v\n", c.counter)
			}
			c.counter++
			n := len(c.send)
			for i := 0; i < n; i++ {
				s.Debugf("tx_tcp: packet sent %v\n", c.counter)
				c.conn.Write(<-c.send)
				_, e := c.conn.Read(tmp)
				if e != nil {
					if e != io.EOF {
						s.Errorw("tx_tcp:", "err", e)
					}
				} else {
					s.Debugf("tx_tcp: got ack %v\n", c.counter)
				}
				c.counter++
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
		s.Errorw("tx_tcp:", "err", e)
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
