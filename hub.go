package main

import (
	"strings"
	"sync"
	"time"
)

type Hub struct {
	connections map[*Connection]bool
	broadcast   chan *message
	register    chan *Connection
	unregister  chan *Connection
	bans        chan Userid
	ipbans      chan string
	getips      chan useridips
	users       map[Userid]*User
	refreshuser chan Userid
	getuser     chan *uidnickfeaturechan
	subs        map[*Connection]bool
	submode     bool
	sublock     chan bool
	sync.RWMutex
}

type uidchan struct {
	userid Userid
	c      chan *User
}

type uiduser struct {
	userid Userid
	user   *User
}

type useridips struct {
	userid Userid
	c      chan []string
}

var hub = Hub{
	connections: make(map[*Connection]bool),
	broadcast:   make(chan *message, BROADCASTCHANNELSIZE),
	register:    make(chan *Connection),
	unregister:  make(chan *Connection),
	bans:        make(chan Userid),
	ipbans:      make(chan string),
	getips:      make(chan useridips),
	users:       make(map[Userid]*User),
	refreshuser: make(chan Userid),
	getuser:     make(chan *uidnickfeaturechan),
	subs:        make(map[*Connection]bool),
	submode:     false,
	sublock:     make(chan bool),
	RWMutex:     sync.RWMutex{},
}

func initHub() {
	go hub.run()
}

func (hub *Hub) run() {
	pinger := time.NewTicker(PINGINTERVAL)
	for {
		select {
		case c := <-hub.register:
			hub.connections[c] = true
			if c.user != nil {
				if c.user.isSubscriber() {
					hub.subs[c] = true
				}
				if _, ok := hub.users[c.user.id]; !ok {
					hub.users[c.user.id] = c.user
				}
			}
		case c := <-hub.unregister:
			hub.remove(c)
		case userid := <-hub.refreshuser:
			if u, ok := hub.users[userid]; ok {
				u.RLock()
				for _, c := range u.connections {
					c.Refresh()
				}
				u.RUnlock()
			}
		case d := <-hub.getuser:
			if u, ok := hub.users[d.userid]; ok {
				d.c <- u
			} else {
				u = &User{
					id:              d.userid,
					nick:            d.nick,
					features:        BitField{},
					lastmessage:     nil,
					lastmessagetime: time.Time{},
					lastactive:      time.Now(),
					delayscale:      1,
					simplified:      nil,
					connections:     nil,
					RWMutex:         sync.RWMutex{},
				}
				u.setFeatures(d.features)
				u.assembleSimplifiedUser()
				hub.users[d.userid] = u
				protected := u.isProtected()
				addnickuid <- &nickuidprot{strings.ToLower(u.nick), uidprot{d.userid, protected}}
				d.c <- u
			}
		case userid := <-hub.bans:
			if u, ok := hub.users[userid]; ok {
				u.RLock()
				for _, c := range u.connections {
					c.Banned()
				}
				u.RUnlock()
			}
		case stringip := <-hub.ipbans:
			for c := range hub.connections {
				addr := c.socket.Request().RemoteAddr
				pos := strings.LastIndex(addr, ":")
				ip := addr[:pos]
				if ip == stringip {
					c.Banned()
				}
			}
		case d := <-hub.getips:
			ips := make([]string, 0, 3)
			if u, ok := hub.users[d.userid]; ok {
				u.RLock()
				for _, c := range u.connections {
					addr := c.socket.Request().RemoteAddr
					pos := strings.LastIndex(addr, ":")
					ip := addr[:pos]
					ips = append(ips, ip)
				}
				u.RUnlock()
			}
			d.c <- ips
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
		case d := <-hub.sublock:
			hub.Lock()
			hub.submode = d
			hub.Unlock()
		}
	}
}

func (hub *Hub) remove(c *Connection) {
	if c.user != nil {
		c.user.RLock()
		if len(hub.users[c.user.id].connections) == 0 {
			delete(hub.subs, c)
		}
		c.user.RUnlock()
	}
	delete(hub.connections, c)
	c.socket.Close()
}

func (hub *Hub) getIPsForUserid(userid Userid) []string {
	// TODO if user not connected get ips from redis, also means need to store ips there
	c := make(chan []string, 1)
	hub.getips <- useridips{userid, c}
	return <-c
}

func (hub *Hub) canUserSpeak(c *Connection) bool {
	hub.RLock()
	defer hub.RUnlock()

	if !hub.submode || c.user.isSubscriber() {
		return true
	}

	return false

}

func removeConnFromArray(a []*Connection, v *Connection) (ret []*Connection) {
	// https://code.google.com/p/go-wiki/wiki/SliceTricks
	ret = a
	for i, va := range a {
		if va == v {
			j := i + 1
			if len(a)-1 < j {
				// pop
				a[i] = nil
				ret = a[:i]
			} else {
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
