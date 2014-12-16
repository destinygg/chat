package main

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/tideland/godm/v3/redis"
)

type Hub struct {
	connections map[*Connection]bool
	broadcast   chan *message
	notify      chan *NotifyOut
	register    chan *Connection
	unregister  chan *Connection
	bans        chan Userid
	ipbans      chan string
	getips      chan useridips
	users       map[Userid]*User
	refreshuser chan Userid
}

type useridips struct {
	userid Userid
	c      chan []string
}

var hub = Hub{
	connections: make(map[*Connection]bool),
	broadcast:   make(chan *message, BROADCASTCHANNELSIZE),
	notify:      make(chan *NotifyOut, BROADCASTCHANNELSIZE),
	register:    make(chan *Connection, 256),
	unregister:  make(chan *Connection),
	bans:        make(chan Userid, 4),
	ipbans:      make(chan string, 4),
	getips:      make(chan useridips),
	users:       make(map[Userid]*User),
	refreshuser: make(chan Userid, 4),
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
				addr := c.socket.UnderlyingConn().RemoteAddr().String()
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
					addr := c.socket.UnderlyingConn().RemoteAddr().String()
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
		case n := <-hub.notify:
			for c, _ := range hub.connections {
				if c.user != nil && (c.user.id == n.from || c.user.id == n.notify.Targetuserid) {
					if len(c.sendmarshalled) < SENDCHANNELSIZE {
						c.sendmarshalled <- &n.message
					}
				}
			}
		// timeout handling
		case t := <-pinger.C:
			for c := range hub.connections {
				if c.ping != nil && len(c.ping) < 2 {
					c.ping <- t
				} else if c.ping != nil {
					close(c.ping)
					c.ping = nil
				}
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
	state.RLock()
	defer state.RUnlock()

	if !state.submode || c.user.isSubscriber() {
		return true
	}

	return false
}

func (hub *Hub) toggleSubmode(enabled bool) {
	state.Lock()
	defer state.Unlock()

	state.submode = enabled
	state.save()
}

func initBroadcast(redisdb int64) {
	go setupBroadcast(redisdb)
}

func setupBroadcast(redisdb int64) {
	setupRedisSubscription("broadcast", redisdb, func(result *redis.PublishedValue) {
		var bc EventDataIn
		err := json.Unmarshal(result.Value.Bytes(), &bc)
		if err != nil {
			D("unable to unmarshal broadcast message", result.Value.String())
			return
		}

		data := &EventDataOut{}
		data.Timestamp = unixMilliTime()
		data.Data = bc.Data
		m, _ := Marshal(data)
		hub.broadcast <- &message{
			event: "BROADCAST",
			data:  m,
		}
		db.insertChatEvent(Userid(0), "BROADCAST", data)
	})
}
