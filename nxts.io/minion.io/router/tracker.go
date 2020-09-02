/*
 * tracker.go: maintains list of clients
 *
 * Davi Gupta, davigupta@gmail.com, Sep 2020
 */
package router

import (
    "go.uber.org/zap"
)

type Tracker struct {
    clients map[*Client]bool
    register chan *Client
    unregister chan *Client
}

func newTracker() *Tracker {
    return &Tracker {
        register: make(chan *Client),
        unregister: make(chan *Client),
        clients: make(map[*Client]bool),
    }
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
        }
    }
}
