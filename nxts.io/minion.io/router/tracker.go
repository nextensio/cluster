/*
 * tracker.go: maintains list of clients
 *
 * Davi Gupta, davigupta@gmail.com, Sep 2020
 */
package router

import (
    "fmt"
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

func (t *Tracker) run() {
    for {
        select {
        case client := <- t.register:
            fmt.Println("registering client")
            t.clients[client] = true
        case client := <- t.unregister:
            fmt.Println("unregistering client")
            if _, ok := t.clients[client]; ok {
                delete(t.clients, client)
                close(client.send)
            }
        }
    }
}
