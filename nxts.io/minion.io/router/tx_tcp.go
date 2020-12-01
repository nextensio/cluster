/*
 * tx_tcp.go: Tx Packet Processor
 *
 * Author: Davi Gupta (davigupta@gmail.com), Sep 2020
 */

package router

import (
	"go.uber.org/zap"
	"io"
	"minion.io/common"
	"net"
	"strconv"
	"strings"
	"time"
)

type TcpClConn struct {
	conn    net.Conn
	last    time.Time
	send    chan common.Queue
	name    string
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
				break
			}
			plen, err := c.conn.Write(msg.Pak)
			if err != nil {
				s.Errorf("tx_tcp: error sending pak of len=%v to %s - %v", plen, c.name, err)
				return
			}
			s.Debugf("tx_tcp: packet %v of len=%v sent to %s", c.counter, plen, c.name)
			c.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
			alen, e := c.conn.Read(tmp)
			if e != nil {
				if e != io.EOF {
					s.Errorf("tx_tcp: connection read error - %v", e)
				} else {
					s.Errorf("tx_tcp: EOF rcvd, closing connection to %v", c.name)
					return
				}
			} else {
				s.Debugf("tx_tcp: got ACK %v (len=%v) from %s", c.counter, alen, c.name)
			}
			c.counter++
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
		s.Errorf("tx_tcp: TCP dial err to %s - %v", servAddr, e)
		return nil, e
	}
	v := TcpClConn{conn: conn, last: time.Now(),
		send: make(chan common.Queue, common.MaxQueueSize), name: name}
	s.Debugf("tx_tcp: dial tcp connection to %v", servAddr)
	go v.txHandler(t, s)

	return &v, e
}

func TcpTxInit() {
}
