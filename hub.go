package main

import (
	"net"
	"sync"
	"time"
)

type Hub struct {
	connections  map[*Connection]bool
	broadcast    chan *message
	register     chan *Connection
	unregister   chan *Connection
	mutes        chan Userid
	bans         chan Userid
	ipbans       chan net.IP
	stringipbans chan string
	users        map[Userid][]*Connection
	subs         map[*Connection]bool
	submode      bool
	sublock      sync.RWMutex
	sync.RWMutex
}

var hub = Hub{
	broadcast:    make(chan *message, BROADCASTCHANNELSIZE),
	register:     make(chan *Connection),
	unregister:   make(chan *Connection, BROADCASTCHANNELSIZE),
	connections:  make(map[*Connection]bool),
	mutes:        make(chan Userid),
	bans:         make(chan Userid, 512),
	ipbans:       make(chan net.IP, 512),
	stringipbans: make(chan string, 512),
	users:        make(map[Userid][]*Connection),
	subs:         make(map[*Connection]bool),
	submode:      false,
	sublock:      sync.RWMutex{},
	RWMutex:      sync.RWMutex{},
}

// run is meant to be run in a goroutine, handling all clients connected
func (hub *Hub) run() {
	pinger := time.NewTicker(PINGINTERVAL)
	for {
		select {
		case c := <-hub.register:
			hub.Lock()
			hub.connections[c] = true
			if c.user != nil {
				if c.user.isSubscriber() {
					hub.subs[c] = true
				}
				if _, ok := hub.users[c.user.id]; !ok {
					hub.users[c.user.id] = make([]*Connection, 1, 2)
					hub.users[c.user.id][0] = c
				} else {
					if len(hub.users[c.user.id]) <= 2 {
						hub.users[c.user.id] = append(hub.users[c.user.id], c)
						conncount := len(hub.users[c.user.id])
						for _, v := range hub.users[c.user.id] {
							v.user.simplified.Connections = uint8(conncount)
						}
					} else {
						c.SendError("toomanyconnections")
						c.stop <- true
					}
				}
			}
			hub.Unlock()
		case c := <-hub.unregister:
			hub.remove(c)
		case userid := <-hub.mutes:
			hub.Lock()
			for _, c := range hub.users[userid] {
				c.Muted()
			}
			hub.Unlock()
		case userid := <-hub.bans:
			hub.Lock()
			for _, c := range hub.users[userid] {
				c.Banned()
			}
			hub.Unlock()
		case ip := <-hub.ipbans:
			hub.Lock()
			for c := range hub.connections {
				if connip := c.socket.RemoteAddr().(*net.TCPAddr).IP; connip.Equal(ip) {
					c.Banned()
				}
			}
			hub.Unlock()
		case stringip := <-hub.stringipbans:
			hub.Lock()
			for c := range hub.connections {
				if connip := c.socket.RemoteAddr().(*net.TCPAddr).IP.String(); connip == stringip {
					c.Banned()
				}
			}
			hub.Unlock()
		case message := <-hub.broadcast:
			for c := range hub.connections {
				if len(c.send) < SENDCHANNELSIZE {
					c.send <- message
				}
			}
		// timeout handling
		case t := <-pinger.C:
			for c := range hub.connections {
				if len(c.ping) < 2 {
					c.ping <- t
				}
			}
		}
	}
}

func (hub *Hub) remove(c *Connection) {

	hub.Lock()
	defer hub.Unlock()

	if c.user != nil {
		hub.users[c.user.id] = removeConnFromArray(hub.users[c.user.id], c)
		if len(hub.users[c.user.id]) == 0 {
			delete(hub.subs, c)
		}
	}
	delete(hub.connections, c)

	c.socket.Close()
}

func (hub *Hub) getIPsForUserid(userid Userid) map[string]net.IP {
	hub.RLock()
	defer hub.RUnlock()
	ips := make(map[string]net.IP)
	for c := range hub.connections {
		if c.user == nil || c.user.id != userid {
			continue
		}
		ip := c.socket.RemoteAddr().(*net.TCPAddr).IP
		ips[ip.String()] = ip
	}
	return ips
}

func (hub *Hub) canUserSpeak(c *Connection) bool {
	hub.sublock.RLock()
	defer hub.sublock.RUnlock()

	if !hub.submode || c.user.isSubscriber() {
		return true
	}

	return false

}

func removeConnFromArray(a []*Connection, v *Connection) (ret []*Connection) {
	ret = a
	for i, va := range a {
		if va == v {
			j := i + 1
			va.user.simplified.Connections--
			if len(a)-1 < j {
				// pop
				a[i] = nil
				ret = a[:i]
			} else {
				// https://code.google.com/p/go-wiki/wiki/SliceTricks
				// cut
				copy(a[i:], a[j:])
				for k, n := len(a)-j+i, len(a); k < n; k++ {
					a[k] = nil
				}
				ret = a[:len(a)-j+i]
			}
		}
	}
	return
}
