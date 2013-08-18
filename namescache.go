package main

import (
	"time"
)

type namesCache struct {
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
			if u, ok := nc.users[user.id]; ok {
				u.Lock()
				u.simplified.Nick = user.nick
				u.simplified.Features = user.simplified.Features
				u.nick = user.nick
				u.features = user.features
				u.Unlock()
				nc.marshalNames()
			}
		case uc := <-nc.adduser:
			nc.usercount++
			if u, ok := nc.users[uc.user.id]; ok {
				u.Lock()
				u.simplified.Connections++
				u.Unlock()
			} else {
				su := &SimplifiedUser{
					Nick:        uc.user.nick,
					Features:    uc.user.simplified.Features,
					Connections: 1,
				}
				uc.user.simplified = su
				nc.users[uc.user.id] = uc.user
			}
			uc.c <- nc.users[uc.user.id]
			nc.marshalNames()
		case user := <-nc.discuser:
			nc.usercount--
			if u, ok := nc.users[user.id]; ok {
				u.Lock()
				u.simplified.Connections--
				if u.simplified.Connections <= 0 {
					delete(nc.users, user.id)
				}
				u.Unlock()
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
	users := make([]*SimplifiedUser, 0, len(nc.users))
	for _, u := range nc.users {
		u.RLock()
		users = append(users, u.simplified)
	}

	nc.marshallednames, _ = Marshal(&NamesOut{
		Users:       users,
		Connections: nc.usercount,
	})

	for _, u := range nc.users {
		u.RUnlock()
	}
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
	if user != nil {
		nc.discuser <- user
	} else {
		nc.removeconnection <- true
	}
}

func (nc *namesCache) refresh(user *User) {
	nc.refreshuser <- user
}

func (nc *namesCache) addConnection() {
	nc.addconnection <- true
}
