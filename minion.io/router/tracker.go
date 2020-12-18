/*
 * tracker.go: maintains list of clients
 *
 * Author: Davi Gupta (davigupta@gmail.com), Sep 2020
 */
package router

import (
	"net"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
	"minion.io/common"
	"minion.io/consul"
)

var serviceLeft map[string]*WsClient
var mL sync.RWMutex
var serviceRight map[string]*http.Client
var mR sync.RWMutex

// Initialise name lookup DB available on websocket side
func ClientInit() {
	serviceLeft = make(map[string]*WsClient)
	serviceRight = make(map[string]*http.Client)
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

func LookupLeftService(name string) *WsClient {
	mL.RLock()
	v := serviceLeft[name]
	mL.RUnlock()
	return v
}

func newHttpClient() *http.Client {
	var netTransport = &http.Transport{
		Dial: (&net.Dialer{
			Timeout:   2 * time.Second,
			KeepAlive: 30 * time.Second,
		}).Dial,
		TLSHandshakeTimeout: 2 * time.Second,
	}
	netClient := &http.Client{
		Timeout:   time.Second * 2,
		Transport: netTransport,
	}
	return netClient
}

func getHttp(name string) *http.Client {
	mR.RLock()
	v := serviceRight[name]
	mR.RUnlock()

	if v != nil {
		return v
	}

	mR.Lock()
	v = serviceRight[name]
	if v == nil {
		v = newHttpClient()
		serviceRight[name] = v
	}
	mR.Unlock()

	return v
}

func delHttp(name string) {
	mR.Lock()
	delete(serviceRight, name)
	mR.Unlock()
}
