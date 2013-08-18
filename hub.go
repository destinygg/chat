package main

import (
	"strings"
	"sync/atomic"
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
	submode     uint32
	sublock     chan bool
}

type useridips struct {
	userid Userid
	c      chan []string
}

var hub = Hub{
	connections: make(map[*Connection]bool),
	broadcast:   make(chan *message, BROADCASTCHANNELSIZE),
	register:    make(chan *Connection, 256),
	unregister:  make(chan *Connection),
	bans:        make(chan Userid, 4),
	ipbans:      make(chan string, 4),
	getips:      make(chan useridips),
	users:       make(map[Userid]*User),
	refreshuser: make(chan Userid, 4),
	submode:     0,
}

func initHub() {
	go hub.run()
}

func (hub *Hub) run() {
	pinger := time.NewTicker(PINGINTERVAL)
	t := time.NewTicker(time.Minute)
	cp := watchdog.register("hub thread", time.Minute)
	defer watchdog.unregister("hub thread")

	for {
		select {
		case <-t.C:
			cp <- true
		case c := <-hub.register:
			hub.connections[c] = true
		case c := <-hub.unregister:
			delete(hub.connections, c)
		case userid := <-hub.refreshuser:
			for c, _ := range hub.connections {
				if c.user != nil && c.user.id == userid {
					c.Refresh()
				}
			}
		case userid := <-hub.bans:
			for c, _ := range hub.connections {
				if c.user != nil && c.user.id == userid {
					c.Banned()
				}
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
			for c, _ := range hub.connections {
				if c.user != nil && c.user.id == d.userid {
					addr := c.socket.Request().RemoteAddr
					pos := strings.LastIndex(addr, ":")
					ip := addr[:pos]
					ips = append(ips, ip)
				}
			}
			d.c <- ips
		case message := <-hub.broadcast:
			for c := range hub.connections {
				if len(c.sendmarshalled) < SENDCHANNELSIZE {
					c.sendmarshalled <- message
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
			if d {
				atomic.StoreUint32(&hub.submode, 1)
			} else {
				atomic.StoreUint32(&hub.submode, 0)
			}
		}
	}
}

func (hub *Hub) getIPsForUserid(userid Userid) []string {
	c := make(chan []string, 1)
	hub.getips <- useridips{userid, c}
	return <-c
}

func (hub *Hub) canUserSpeak(c *Connection) bool {
	submode := atomic.LoadUint32(&hub.submode)
	if submode == 0 || c.user.isSubscriber() {
		return true
	}

	return false
}

func (hub *Hub) toggleSubmode(enabled bool) {
	var val uint32
	if enabled {
		val = 1
	}
	atomic.StoreUint32(&hub.submode, val)
}
