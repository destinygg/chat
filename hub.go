package main

import (
	"bytes"
	"crypto/md5"
	"io"
	"net"
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

type NamesOut struct {
	Users       *[]*SimplifiedUser `json:"users"`
	Connections int                `json:"connectioncount"`
}

var (
	namecache *NamesOut
	namehash  []byte
)

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
					hub.users[c.user.id].connections = make([]*Connection, 1, 3)
					hub.users[c.user.id].connections[0] = c
					hub.generateNames()
				} else {
					if len(hub.users[c.user.id].connections) <= 3 {
						hub.users[c.user.id].connections = append(hub.users[c.user.id].connections, c)
						conncount := len(hub.users[c.user.id].connections)
						hub.users[c.user.id].simplified.Connections = uint8(conncount)
					} else {
						c.SendError("toomanyconnections")
						c.stop <- true
						continue
					}
				}
			}
			if namecache != nil {
				namecache.Connections = len(hub.connections)
				c.Names(namecache)
			}
		case c := <-hub.unregister:
			hub.remove(c)
		case userid := <-hub.refreshuser:
			if u, ok := hub.users[userid]; ok {
				for _, c := range u.connections {
					c.Refresh()
				}
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
				for _, c := range u.connections {
					c.Banned()
				}
			}
		case stringip := <-hub.ipbans:
			for c := range hub.connections {
				if connip := c.socket.RemoteAddr().(*net.TCPAddr).IP.String(); connip == stringip {
					c.Banned()
				}
			}
		case d := <-hub.getips:
			ips := make([]string, 0, 3)
			if users, ok := hub.users[d.userid]; ok {
				for _, c := range users.connections {
					ip := c.socket.RemoteAddr().(*net.TCPAddr).IP.String()
					ips = append(ips, ip)
				}
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
		hub.users[c.user.id].connections = removeConnFromArray(hub.users[c.user.id].connections, c)
		if len(hub.users[c.user.id].connections) == 0 {
			delete(hub.subs, c)
		}
	}
	delete(hub.connections, c)

	c.socket.Close()
}

func (hub *Hub) getIPsForUserid(userid Userid) []string {
	// TODO if user not connected get ips from redis, also means need to store ips there
	m := make(chan []string)
	hub.getips <- useridips{userid, m}
	return <-m
}

func (hub *Hub) canUserSpeak(c *Connection) bool {
	hub.RLock()
	defer hub.RUnlock()

	if !hub.submode || c.user.isSubscriber() {
		return true
	}

	return false

}

func (hub *Hub) generateNames() {
	hash := md5.New()
	for _, v := range hub.users {
		io.WriteString(hash, v.nick)
	}

	hashsum := hash.Sum(nil)
	if namehash != nil && bytes.Equal(hashsum, namehash) {
		return
	}

	users := make([]*SimplifiedUser, 0, len(hub.users))
	for _, v := range hub.users {
		users = append(users, v.simplified)
	}

	namehash = hashsum
	namecache = &NamesOut{&users, len(hub.connections)}
}

func removeConnFromArray(a []*Connection, v *Connection) (ret []*Connection) {
	// https://code.google.com/p/go-wiki/wiki/SliceTricks
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
