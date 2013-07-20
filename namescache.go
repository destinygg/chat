package main

import (
	"time"
)

type NamesOut struct {
	Users       []*SimplifiedUser `json:"users"`
	Connections uint32            `json:"connectioncount"`
}

var (
	names           = make(map[Userid]*SimplifiedUser)
	usercount       uint32
	marshallednames []byte
)

var (
	adduser          = make(chan *User)
	discuser         = make(chan *User)
	addconnection    = make(chan bool)
	removeconnection = make(chan bool)
	getnames         = make(chan chan []byte)
)

func initNamesCache() {
	go (func() {
		t := time.NewTicker(time.Minute)
		for {
			select {
			case <-t.C:
				for k, v := range names {
					if v.Connections <= 0 {
						delete(names, k)
					}
				}
			case user := <-adduser:
				usercount++
				if su, ok := names[user.id]; ok {
					su.Connections++
				} else {
					names[user.id] = &SimplifiedUser{
						Nick:        user.nick,
						Features:    user.simplified.Features,
						Connections: 1,
					}
				}
				marshalNames()
			case user := <-discuser:
				usercount--
				if su, ok := names[user.id]; ok {
					su.Connections--
				}
				marshalNames()
			case <-addconnection:
				usercount++
				marshalNames()
			case <-removeconnection:
				usercount--
				marshalNames()
			case r := <-getnames:
				r <- marshallednames
			}
		}
	})()
}

func marshalNames() {

	users := make([]*SimplifiedUser, 0, len(names))
	for _, su := range names {
		if su.Connections > 0 {
			users = append(users, su)
		}
	}

	marshallednames, _ = Marshal(&NamesOut{
		Users:       users,
		Connections: usercount,
	})

}

func getNames() []byte {
	reply := make(chan []byte, 1)
	getnames <- reply
	return <-reply
}

func addToNameCache(user *User) {
	adduser <- user
}

func userDisconnect(user *User) {
	discuser <- user
}

func addConnection() {
	addconnection <- true
}

func removeConnection() {
	removeconnection <- true
}
