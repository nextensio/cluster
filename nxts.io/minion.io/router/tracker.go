/*
 * tracker.go: maintains list of clients
 *
 * Author: Davi Gupta (davigupta@gmail.com), Sep 2020
 */
package router

import (
	"go.uber.org/zap"
	"minion.io/common"
	"minion.io/consul"
	"sync"
)

type Tracker struct {
	ws         map[*WsClient]bool
	register   chan *WsClient
	unregister chan *WsClient
	add        chan *WsClient
	del        chan *WsClient
	connect    chan string
	conn       chan *TcpClConn
	tcpTx      map[*TcpClConn]bool
	close      chan *TcpClConn
}

type TcpRxTracker struct {
	tcpRx      map[*TcpSeConn]bool
	register   chan *TcpSeConn
	unregister chan *TcpSeConn
}

var serviceLeft map[string]*WsClient
var destRight map[string]*TcpClConn
var mL sync.RWMutex
var mR sync.RWMutex

// Initialise name lookup DB available on websocket side
func clientInit() {
	serviceLeft = make(map[string]*WsClient)
	destRight = make(map[string]*TcpClConn)
}

func NewTracker() *Tracker {
	clientInit()
	return &Tracker{
		register:   make(chan *WsClient),
		unregister: make(chan *WsClient),
		ws:         make(map[*WsClient]bool),
		add:        make(chan *WsClient),
		del:        make(chan *WsClient),
		tcpTx:      make(map[*TcpClConn]bool),
		close:      make(chan *TcpClConn),
		connect:    make(chan string),
		conn:       make(chan *TcpClConn),
	}
}

func addService(c *WsClient, s *zap.SugaredLogger) {
	// register with Consul
	for i := 0; i < c.num; i++ {
		v := serviceLeft[c.name[i]]
		if v != nil {
			c.name_reg[i] = false
			s.Debugf("tracker: addService detected duplicate ws serviceLeft %s\n", c.name[i])
		} else {
			// Do not register last one (which is localhost)
			if i < c.num-1 {
				c.name_reg[i] = true
				consul.RegisterConsul([common.MaxService]string{c.name[i]}, s)
				s.Debugf("tracker: registered ws service %v in Consul", c.name[i])
			}
			mL.Lock()
			serviceLeft[c.name[i]] = c
			mL.Unlock()
			s.Debugf("tracker: addService added %s to ws serviceLeft", c.name[i])
		}
	}
	s.Debugf("tracker: addService added %v ws service(s) for %s %s", c.num, c.clitype, c.uuid)
}

func delService(c *WsClient, s *zap.SugaredLogger) {
	mL.Lock()
	for i := 0; i < c.num; i++ {
		if c.name_reg[i] {
			delete(serviceLeft, c.name[i])
			s.Debugf("tracker: deleted ws serviceLeft %v", c.name[i])
		}
	}
	mL.Unlock()
	// deregister with Consul, skip last entry (which is localhost)
	for i := 0; i < c.num-1; i++ {
		if c.name_reg[i] {
			consul.DeRegisterConsul([common.MaxService]string{c.name[i]}, s)
			s.Debugf("tracker: deregistered ws service %v from Consul", c.name[i])
		}
	}
}

func addDest(c *TcpClConn, s *zap.SugaredLogger) {
	v := destRight[c.name]
	if v != nil {
		s.Debugf("tracker: duplicate destRight %v\n", c.name)
		return
	}
	mR.Lock()
	destRight[c.name] = c
	mR.Unlock()
	s.Debugf("tracker: added %v TCP conn to destRight", c.name)
}

func delDest(c *TcpClConn, s *zap.SugaredLogger) {
	mR.Lock()
	delete(destRight, c.name)
	mR.Unlock()
	s.Debugf("tracker: deleted %v TCP conn from destRight", c.name)
}

func LookupLeftService(name string) *WsClient {
	mL.RLock()
	v := serviceLeft[name]
	mL.RUnlock()
	return v
}

func LookupRightDest(name string) *TcpClConn {
	mR.RLock()
	v := destRight[name]
	mR.RUnlock()
	return v
}

func (t *Tracker) run(s *zap.SugaredLogger) {
	for {
		select {
		case client := <-t.register:
			t.ws[client] = true
			s.Debug("tracker: registering client")
		case client := <-t.unregister:
			if _, ok := t.ws[client]; ok {
				delete(t.ws, client)
				close(client.send)
			}
			s.Debug("tracker: unregistering client")
		case client := <-t.add:
			addService(client, s)
		case client := <-t.del:
			delService(client, s)
		case name := <-t.connect:
			tcpConn, e := TcpClient(t, name, s)
			if e == nil {
				t.tcpTx[tcpConn] = true
				addDest(tcpConn, s)
				s.Debugf("tracker: connected to %v", tcpConn.name)
			}
			t.conn <- tcpConn
		case tcpConn := <-t.close:
			delDest(tcpConn, s)
			if _, ok := t.tcpTx[tcpConn]; ok {
				s.Debugf("tracker: deleting connection to %v", tcpConn.name)
				delete(t.tcpTx, tcpConn)
				close(tcpConn.send)
			}
		}
	}
}

func NewTcpRxTracker() *TcpRxTracker {
	return &TcpRxTracker{
		register:   make(chan *TcpSeConn),
		unregister: make(chan *TcpSeConn),
		tcpRx:      make(map[*TcpSeConn]bool),
	}
}

func (t *TcpRxTracker) run(s *zap.SugaredLogger) {
	for {
		select {
		case client := <-t.register:
			t.tcpRx[client] = true
			s.Debug("tracker: registering tcp rx client")
		case client := <-t.unregister:
			if _, ok := t.tcpRx[client]; ok {
				delete(t.tcpRx, client)
			}
			s.Debug("tracker: unregistering tcp rx client")
		}
	}
}
