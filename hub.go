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
	users        map[Userid]*SimplifiedUser
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
	users:        make(map[Userid]*SimplifiedUser),
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
				if _, ok := hub.users[c.user.id]; !ok {
					hub.users[c.user.id] = c.getSimplifiedUser()
				} else {
					hub.users[c.user.id].Connections++
				}
			}
			hub.Unlock()
		case c := <-hub.unregister:
			hub.remove(c)
		case userid := <-hub.mutes:
			for c := range hub.connections {
				if c.user != nil && c.user.id == userid {
					c.Muted()
				}
			}
		case userid := <-hub.bans:
			for c := range hub.connections {
				if c.user != nil && c.user.id == userid {
					c.Banned()
				}
			}
		case ip := <-hub.ipbans:
			for c := range hub.connections {
				if connip := c.socket.RemoteAddr().(*net.TCPAddr).IP; connip.Equal(ip) {
					c.Banned()
				}
			}
		case stringip := <-hub.stringipbans:
			for c := range hub.connections {
				if connip := c.socket.RemoteAddr().(*net.TCPAddr).IP.String(); connip == stringip {
					c.Banned()
				}
			}
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
		simplified := hub.users[c.user.id]
		simplified.Connections--
		if simplified.Connections <= 0 {
			delete(hub.users, c.user.id)
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
