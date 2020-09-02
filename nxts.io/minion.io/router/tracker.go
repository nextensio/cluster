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
    clients map[*WsClient]bool
    register chan *WsClient
    unregister chan *WsClient
    add chan *WsClient
    del chan *WsClient
}

var serviceLeft map[string]*WsClient
var mL sync.RWMutex

// Initialise name lookup DB available on websocket side
func clientInit() {
    serviceLeft = make(map[string]*WsClient)
}

func newTracker() *Tracker {
    clientInit()
    return &Tracker {
        register: make(chan *WsClient),
        unregister: make(chan *WsClient),
        clients: make(map[*WsClient]bool),
        add: make(chan *WsClient),
        del: make(chan *WsClient),
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

func LookupLeftService(name string) *WsClient {
    mL.RLock()
    v := serviceLeft[name]
    mL.RUnlock()
    return v
}

func LookupRightService(name string) *WsClient {
    return nil
}

func (t *Tracker) run(s *zap.SugaredLogger) {
    for {
        select {
        case client := <- t.register:
            s.Debug("tracker: registering client")
            t.clients[client] = true
        case client := <- t.unregister:
            s.Debug("tracker: unregistering client")
            if _, ok := t.clients[client]; ok {
                delete(t.clients, client)
                close(client.send)
            }
        case client := <- t.add:
            s.Debug("tracker: registering services")
            addService(client, s)
        case client := <- t.del:
            s.Debug("tracker: deregistering services")
            delService(client, s)
        }
    }
}
