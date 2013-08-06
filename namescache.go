package main

import (
	"time"
)

type namesCache struct {
	names            map[Userid]*SimplifiedUser
	marshallednames  []byte
	usercount        uint32
	adduser          chan *User
	refreshuser      chan *User
	discuser         chan *User
	addconnection    chan bool
	removeconnection chan bool
	getnames         chan chan []byte
}

type NamesOut struct {
	Users       []*SimplifiedUser `json:"users"`
	Connections uint32            `json:"connectioncount"`
}

var nc = namesCache{
	names:            make(map[Userid]*SimplifiedUser),
	marshallednames:  nil,
	usercount:        0,
	adduser:          make(chan *User),
	refreshuser:      make(chan *User),
	discuser:         make(chan *User),
	addconnection:    make(chan bool),
	removeconnection: make(chan bool),
	getnames:         make(chan chan []byte),
}

func initNamesCache() {
	go (func() {
		t := time.NewTicker(time.Minute)
		cp := registerWatchdog("namescache thread", time.Minute)
		defer unregisterWatchdog("namescache thread")

		for {
			select {
			case <-t.C:
				cp <- true
			case user := <-nc.refreshuser:
				if su, ok := nc.names[user.id]; ok {
					su.Nick = user.nick
					su.Features = user.simplified.Features
				} else {
					nc.names[user.id] = &SimplifiedUser{
						Nick:        user.nick,
						Features:    user.simplified.Features,
						Connections: 0,
					}
				}
			case user := <-nc.adduser:
				nc.usercount++
				if su, ok := nc.names[user.id]; ok {
					su.Connections++
				} else {
					nc.names[user.id] = &SimplifiedUser{
						Nick:        user.nick,
						Features:    user.simplified.Features,
						Connections: 1,
					}
				}
				marshalNames()
			case user := <-nc.discuser:
				nc.usercount--
				if su, ok := nc.names[user.id]; ok {
					su.Connections--
					if su.Connections <= 0 {
						delete(nc.names, user.id)
					}
				}
				marshalNames()
			case <-nc.addconnection:
				nc.usercount++
				marshalNames()
			case <-nc.removeconnection:
				nc.usercount--
				marshalNames()
			case r := <-nc.getnames:
				r <- nc.marshallednames
			}
		}
	})()
}

func marshalNames() {

	users := make([]*SimplifiedUser, 0, len(nc.names))
	for _, su := range nc.names {
		if su.Connections > 0 {
			users = append(users, su)
		}
	}

	nc.marshallednames, _ = Marshal(&NamesOut{
		Users:       users,
		Connections: nc.usercount,
	})

}

func getNames() []byte {
	reply := make(chan []byte, 1)
	nc.getnames <- reply
	return <-reply
}

func addToNameCache(user *User) {
	nc.adduser <- user
}

func userDisconnect(user *User) {
	nc.discuser <- user
}

func userRefresh(user *User) {
	nc.refreshuser <- user
}

func addConnection() {
	nc.addconnection <- true
}

func removeConnection() {
	nc.removeconnection <- true
}
