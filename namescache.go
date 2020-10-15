//go:generate ffjson namescache.go
package main

import (
	"sync"
	"sync/atomic"
)

// ffjson: skip
type namesCache struct {
	users           map[Userid]*User
	marshallednames []byte
	usercount       uint32
	ircnames        [][]string
	sync.RWMutex
}

// ffjson: skip
type userChan struct {
	user *User
	c    chan *User
}

type NamesOut struct {
	Users       []*SimplifiedUser `json:"users"`
	Connections uint32            `json:"connectioncount"`
}

var namescache = namesCache{
	users:   make(map[Userid]*User),
	RWMutex: sync.RWMutex{},
}

func initNamesCache() {
}

func (nc *namesCache) getIrcNames() [][]string {
	nc.RLock()
	defer nc.RUnlock()
	return nc.ircnames
}

func (nc *namesCache) marshalNames(updateircnames bool) {
	users := make([]*SimplifiedUser, 0, len(nc.users))
	var allnames []string
	if updateircnames {
		allnames = make([]string, 0, len(nc.users))
	}
	for _, u := range nc.users {
		u.RLock()
		n := atomic.LoadInt32(&u.connections)
		if n <= 0 {
			continue
		}
		users = append(users, u.simplified)
		if updateircnames {
			prefix := ""
			switch {
			case u.featureGet(ISADMIN):
				prefix = "~" // +q
			case u.featureGet(ISBOT):
				prefix = "&" // +a
			case u.featureGet(ISMODERATOR):
				prefix = "@" // +o
			case u.featureGet(ISVIP):
				prefix = "%" // +h
			case u.featureGet(ISSUBSCRIBER):
				prefix = "+" // +v
			}
			allnames = append(allnames, prefix+u.nick)
		}
	}

	if updateircnames {
		l := 0
		var namelines [][]string
		var names []string
		for _, name := range allnames {
			if l+len(name) > 400 {
				namelines = append(namelines, names)
				l = 0
				names = nil
			}
			names = append(names, name)
			l += len(name)
		}
		nc.ircnames = namelines
	}

	n := NamesOut{
		Users:       users,
		Connections: nc.usercount,
	}
	nc.marshallednames, _ = n.MarshalJSON()

	cacheConnectedUsers(nc.marshallednames)

	for _, u := range nc.users {
		u.RUnlock()
	}
}

func (nc *namesCache) getNames() []byte {
	nc.RLock()
	defer nc.RUnlock()
	return nc.marshallednames
}

func (nc *namesCache) get(id Userid) *User {
	nc.RLock()
	defer nc.RUnlock()
	u, _ := nc.users[id]
	return u
}

func (nc *namesCache) add(user *User) *User {
	nc.Lock()
	defer nc.Unlock()

	nc.usercount++
	var updateircnames bool
	if u, ok := nc.users[user.id]; ok {
		atomic.AddInt32(&u.connections, 1)
	} else {
		updateircnames = true
		atomic.AddInt32(&user.connections, 1)
		su := &SimplifiedUser{
			Nick:     user.nick,
			Features: user.simplified.Features,
		}
		user.simplified = su
		nc.users[user.id] = user
	}
	nc.marshalNames(updateircnames)
	return nc.users[user.id]
}

func (nc *namesCache) disconnect(user *User) {
	nc.Lock()
	defer nc.Unlock()
	var updateircnames bool

	if user != nil {
		nc.usercount--
		if u, ok := nc.users[user.id]; ok {
			conncount := atomic.AddInt32(&u.connections, -1)
			if conncount <= 0 {
				// we do not delete the users so that the lastmessage is preserved for
				// anti-spam purposes, sadly this means memory usage can only go up
				updateircnames = true
			}
		}

	} else {
		nc.usercount--
	}
	nc.marshalNames(updateircnames)
}

func (nc *namesCache) refresh(user *User) {
	nc.RLock()
	defer nc.RUnlock()

	if u, ok := nc.users[user.id]; ok {
		u.Lock()
		u.simplified.Nick = user.nick
		u.simplified.Features = user.simplified.Features
		u.nick = user.nick
		u.features = user.features
		u.Unlock()
		nc.marshalNames(true)
	}
}

func (nc *namesCache) addConnection() {
	nc.Lock()
	defer nc.Unlock()
	nc.usercount++
	nc.marshalNames(false)
}
