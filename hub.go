package main

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/tideland/godm/v3/redis"
)

type Hub struct {
	connections map[*Connection]bool
	broadcast   chan *message
	privmsg     chan *PrivmsgOut
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
	privmsg:     make(chan *PrivmsgOut, BROADCASTCHANNELSIZE),
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

	for {
		select {
		case c := <-hub.register:
			hub.connections[c] = true
		case c := <-hub.unregister:
			delete(hub.connections, c)
		case userid := <-hub.refreshuser:
			for c, _ := range hub.connections {
				if c.user != nil && c.user.id == userid {
					go c.Refresh()
				}
			}
		case userid := <-hub.bans:
			for c, _ := range hub.connections {
				if c.user != nil && c.user.id == userid {
					go c.Banned()
				}
			}
		case stringip := <-hub.ipbans:
			for c := range hub.connections {
				if c.ip == stringip {
					DP("Found connection to ban with ip", stringip, "user", c.user)
					go c.Banned()
				}
			}
		case d := <-hub.getips:
			ips := make([]string, 0, 3)
			for c, _ := range hub.connections {
				if c.user != nil && c.user.id == d.userid {
					ips = append(ips, c.ip)
				}
			}
			d.c <- ips
		case message := <-hub.broadcast:
			for c := range hub.connections {
				if len(c.sendmarshalled) < SENDCHANNELSIZE {
					c.sendmarshalled <- message
				}
			}
		case p := <-hub.privmsg:
			for c, _ := range hub.connections {
				if c.user != nil && c.user.id == p.targetuid {
					if len(c.sendmarshalled) < SENDCHANNELSIZE {
						c.sendmarshalled <- &p.message
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
	go setupPrivmsg(redisdb)
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

func setupPrivmsg(redisdb int64) {
	setupRedisSubscription("privmsg", redisdb, func(result *redis.PublishedValue) {
		var d struct {
			Username     string
			Targetuserid string
			Message      string
			Messageid    string
		}

		err := json.Unmarshal(result.Value.Bytes(), &d)
		if err != nil {
			D("unable to unmarshal private message", result.Value.String())
			return
		}

		mid, err := strconv.ParseInt(d.Messageid, 10, 64)
		if err != nil {
			D("Unable to parse messageid into number", d.Messageid)
			return
		}

		uid, err := strconv.ParseInt(d.Targetuserid, 10, 64)
		if err != nil {
			D("Unable to parse targetuserid into number", d.Targetuserid)
			return
		}

		p := &PrivmsgOut{
			message: message{
				event: "PRIVMSG",
			},
			Nick:      d.Username,
			targetuid: Userid(uid),
			Data:      d.Message,
			Messageid: mid,
			Timestamp: unixMilliTime(),
		}

		p.message.data, _ = Marshal(p)

		hub.privmsg <- p
	})
}
