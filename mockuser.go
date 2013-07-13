package main

import (
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

type BitField struct {
	data uint32
}

func (b *BitField) Get(bitnum uint32) bool {
	return (b.data & bitnum) != 0
}

func (b *BitField) Set(bitnum uint32) {
	b.data |= bitnum
}

func (b *BitField) NumSet() (c uint8) {
	// Counting bits set, Brian Kernighan's way
	v := b.data
	for c = 0; v != 0; c++ {
		v &= v - 1 // clear the least significant bit set
	}
	return
}

type Userid int64
type User struct {
	id              Userid
	nick            string
	features        *BitField
	lastmessage     []byte
	lastmessagetime time.Time
	lastactive      time.Time
	delayscale      uint8
	simplified      *SimplifiedUser
}

func (u *User) isModerator() bool {
	return u.features.Get(ISMODERATOR | ISADMIN | ISBOT)
}

func (u *User) isSubscriber() bool {
	return u.features.Get(ISSUBSCRIBER | ISADMIN | ISMODERATOR | ISVIP)
}

func (u *User) isBot() bool {
	return u.features.Get(ISBOT)
}

func (u *User) isProtected() bool {
	return u.features.Get(ISADMIN | ISPROTECTED)
}

type nickuid struct {
	id   Userid
	nick string
}
type nickchan struct {
	nick string
	c    chan Userid
}

var (
	nicklookup    = make(map[string]Userid)
	addnickuid    = make(chan *nickuid, 256)
	getuidfornick = make(chan *nickchan, 256)
)

func initUsers() {
	go (func() {
		for {
			select {
			case nu := <-addnickuid:
				nicklookup[nu.nick] = nu.id
			case nc := <-getuidfornick:
				select {
				case nc.c <- nicklookup[nc.nick]:
				default:
				}
			}
		}
	})()
}

func getUseridForNick(nick string) chan Userid {
	c := make(chan Userid)
	getuidfornick <- &nickchan{strings.ToLower(nick), c}
	return c
}

func (u *User) assembleSimplifiedUser() {

	s := make([]string, 0, u.features.NumSet())
	if u.features.Get(ISPROTECTED) {
		s = append(s, "protected")
	}
	if u.features.Get(ISSUBSCRIBER) {
		s = append(s, "subscriber")
	}
	if u.features.Get(ISVIP) {
		s = append(s, "vip")
	}
	if u.features.Get(ISMODERATOR) {
		s = append(s, "moderator")
	}
	if u.features.Get(ISADMIN) {
		s = append(s, "admin")
	}
	if u.features.Get(ISBOT) {
		s = append(s, "bot")
	}

	u.simplified = &SimplifiedUser{
		u.nick,
		&s,
		1,
	}
}

func getUser(r *http.Request) (u *User, banned bool) {

	// TODO check if user is banned, either by IP or uid
	var uid Userid

	hub.RLock()
	for {
		uid = Userid(1 + rand.Intn(9000))
		if _, ok := hub.users[uid]; ok {
			continue
		}
		break
	}
	hub.RUnlock()
	nick := fmt.Sprintf("test%d", uid)
	u = &User{uid, nick, &BitField{}, nil, time.Now(), time.Now(), 1, nil}
	addnickuid <- &nickuid{uid, strings.ToLower(nick)}
	u.features.Set(ISADMIN)
	u.features.Set(ISMODERATOR)
	u.features.Set(ISPROTECTED)
	u.assembleSimplifiedUser()
	D("User connected with id:", u.id, "and nick:", u.nick, "features:", u.simplified.Features, u.isModerator())
	return
}
