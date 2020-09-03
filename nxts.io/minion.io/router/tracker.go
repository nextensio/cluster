/*
 * tracker.go: maintains list of clients
 *
 * Davi Gupta, davigupta@gmail.com, Sep 2020
 */
package router

import (
    "sync"
    "go.uber.org/zap"
    "minion.io/consul"
)

type Tracker struct {
    ws map[*WsClient]bool
    register chan *WsClient
    unregister chan *WsClient
    add chan *WsClient
    del chan *WsClient
    connect chan string
    conn chan *TcpClConn
    tcpTx map[*TcpClConn]bool
    close chan *TcpClConn
}

type TcpRxTracker struct {
    tcpRx map[*TcpSeConn]bool
    register chan *TcpSeConn
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
    return &Tracker {
        register: make(chan *WsClient),
        unregister: make(chan *WsClient),
        ws: make(map[*WsClient]bool),
        add: make(chan *WsClient),
        del: make(chan *WsClient),
        tcpTx: make(map[*TcpClConn]bool),
        close: make(chan *TcpClConn),
        connect: make(chan string),
        conn: make(chan *TcpClConn),
    }
}

func addService(c *WsClient, s *zap.SugaredLogger) {
    // register with Consul
    consul.RegisterConsul(c.name, s)
    mL.Lock()
    for i := 0; i < c.num; i++ {
        serviceLeft[c.name[i]] = c
    }
    mL.Unlock()
    s.Debugf("tracker: services %v", serviceLeft)
}

func delService(c *WsClient, s *zap.SugaredLogger) {
    mL.Lock()
    for i := 0; i < c.num; i++ {
        delete(serviceLeft, c.name[i])
    }
    mL.Unlock()
    s.Debugf("tracker: services %v", serviceLeft)
    // deregister with Consul
    consul.DeRegisterConsul(c.name, s)
}

func addDest(c *TcpClConn, s *zap.SugaredLogger) {
    mR.Lock()
    destRight[c.name] = c
    mR.Unlock()
    s.Debugf("tracker: dest %v", destRight)
}

func delDest(c *TcpClConn, s *zap.SugaredLogger) {
    mR.Lock()
    delete(destRight, c.name)
    mR.Unlock()
    s.Debugf("tracker: dest %v", destRight)
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
        case client := <- t.register:
            s.Debug("tracker: registering client")
            t.ws[client] = true
        case client := <- t.unregister:
            s.Debug("tracker: unregistering client")
            if _, ok := t.ws[client]; ok {
                delete(t.ws, client)
                close(client.send)
            }
        case client := <- t.add:
            s.Debug("tracker: registering services")
            addService(client, s)
        case client := <- t.del:
            s.Debug("tracker: deregistering services")
            delService(client, s)
        case name := <- t.connect:
            s.Debugf("tracker: connect to %s", name)
            tcpConn, e := TcpClient(t, name, s)
            if e == nil {
                t.tcpTx[tcpConn] = true
                addDest(tcpConn, s)
            }
            t.conn <- tcpConn
        case tcpConn := <- t.close:
            s.Debugf("tracker: connect to %v", tcpConn.name)
            delDest(tcpConn, s)
            if _, ok := t.tcpTx[tcpConn]; ok {
                delete(t.tcpTx, tcpConn)
                close(tcpConn.send)
            }
        }
    }
}

func NewTcpRxTracker() *TcpRxTracker {
    return &TcpRxTracker {
        register: make(chan *TcpSeConn),
        unregister: make(chan *TcpSeConn),
        tcpRx: make(map[*TcpSeConn]bool),
    }
}

func (t *TcpRxTracker) run(s *zap.SugaredLogger) {
    for {
        select {
        case client := <- t.register:
            s.Debug("tracker: registering tcp rx client")
            t.tcpRx[client] = true
        case client := <- t.unregister:
            s.Debug("tracker: unregistering tcp rx client")
            if _, ok := t.tcpRx[client]; ok {
                delete(t.tcpRx, client)
            }
        }
    }
}
