package main

import (
	"time"
)

type namesCache struct {
	names            map[Userid]*SimplifiedUser
	users            map[Userid]*User
	marshallednames  []byte
	usercount        uint32
	adduser          chan *userChan
	refreshuser      chan *User
	discuser         chan *User
	addconnection    chan bool
	removeconnection chan bool
	getnames         chan chan []byte
}

type userChan struct {
	user *User
	c    chan *User
}

type NamesOut struct {
	Users       []*SimplifiedUser `json:"users"`
	Connections uint32            `json:"connectioncount"`
}

var namescache = namesCache{
	names:            make(map[Userid]*SimplifiedUser),
	users:            make(map[Userid]*User),
	marshallednames:  nil,
	usercount:        0,
	adduser:          make(chan *userChan),
	refreshuser:      make(chan *User),
	discuser:         make(chan *User),
	addconnection:    make(chan bool),
	removeconnection: make(chan bool),
	getnames:         make(chan chan []byte),
}

func initNamesCache() {
	go namescache.run()
}

func (nc *namesCache) run() {
	t := time.NewTicker(time.Minute)
	cp := watchdog.register("namescache thread", time.Minute)
	defer watchdog.unregister("namescache thread")

	for {
		select {
		case <-t.C:
			cp <- true
		case user := <-nc.refreshuser:
			if su, ok := nc.names[user.id]; ok {
				su.Nick = user.nick
				su.Features = user.features

				nc.users[user.id].Lock()
				nc.users[user.id].nick = user.nick
				nc.users[user.id].features = user.features
				nc.users[user.id].Unlock()
				nc.marshalNames()
			}
		case uc := <-nc.adduser:
			nc.usercount++
			if su, ok := nc.names[uc.user.id]; ok {
				su.Connections++
			} else {
				nc.names[uc.user.id] = &SimplifiedUser{
					Nick:        uc.user.nick,
					Features:    uc.user.features,
					Connections: 1,
				}
				uc.user.simplified = nc.names[uc.user.id]
				nc.users[uc.user.id] = uc.user
			}
			uc.c <- nc.users[uc.user.id]
			nc.marshalNames()
		case user := <-nc.discuser:
			nc.usercount--
			if su, ok := nc.names[user.id]; ok {
				su.Connections--
				if su.Connections <= 0 {
					delete(nc.names, user.id)
					delete(nc.users, user.id)
				}
			}
			nc.marshalNames()
		case <-nc.addconnection:
			nc.usercount++
			nc.marshalNames()
		case <-nc.removeconnection:
			nc.usercount--
			nc.marshalNames()
		case r := <-nc.getnames:
			r <- nc.marshallednames
		}
	}
}

func (nc *namesCache) marshalNames() {
	users := make([]*SimplifiedUser, 0, len(nc.names))
	for _, su := range nc.names {
		users = append(users, su)
	}

	nc.marshallednames, _ = Marshal(&NamesOut{
		Users:       users,
		Connections: nc.usercount,
	})
}

func (nc *namesCache) getNames() []byte {
	reply := make(chan []byte, 1)
	nc.getnames <- reply
	return <-reply
}

func (nc *namesCache) add(user *User) *User {
	c := make(chan *User, 1)
	nc.adduser <- &userChan{user, c}
	return <-c
}

func (nc *namesCache) disconnect(user *User) {
	nc.discuser <- user
}

func (nc *namesCache) refresh(user *User) {
	nc.refreshuser <- user
}

func (nc *namesCache) addConnection() {
	nc.addconnection <- true
}

func (nc *namesCache) removeConnection() {
	nc.removeconnection <- true
}
