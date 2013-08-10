package main

import (
	"bytes"
	"encoding/gob"
	"io/ioutil"
	"time"
)

type Mutes struct {
	users         map[Userid]time.Time
	muteuserid    chan *muteUserid
	isuseridmuted chan *isUseridMuted
	unmuteuserid  chan Userid
}

type isUseridMuted struct {
	userid Userid
	muted  chan bool
}

type muteUserid struct {
	userid Userid
	t      time.Time
}

var (
	mutes = Mutes{
		make(map[Userid]time.Time),
		make(chan *muteUserid),
		make(chan *isUseridMuted),
		make(chan Userid),
	}
)

func initMutes() {
	go mutes.run()
}

func (m *Mutes) run() {
	m.load()
	t := time.NewTicker(time.Minute)
	cp := watchdog.register("mute thread", time.Minute)
	defer watchdog.unregister("mute thread")

	for {
		select {
		case d := <-m.muteuserid:
			m.users[d.userid] = d.t
			m.save()
		case d := <-m.isuseridmuted:
			if t, ok := m.users[d.userid]; ok {
				d.muted <- t.After(time.Now().UTC())
			} else {
				d.muted <- false
			}
		case uid := <-m.unmuteuserid:
			delete(m.users, uid)
			m.save()
		case <-t.C:
			cp <- true
			m.clean()
			m.save()
		}
	}
}

func (m *Mutes) load() {
	n, err := ioutil.ReadFile("mutes.dc")
	if err != nil {
		D("Error while reading from mutes file")
		return
	}
	mb := bytes.NewBuffer(n)
	dec := gob.NewDecoder(mb)
	err = dec.Decode(&m.users)
	if err != nil {
		D("Error decoding mutes file")
	}
}

func (m *Mutes) save() {
	mb := new(bytes.Buffer)
	enc := gob.NewEncoder(mb)
	enc.Encode(&m.users)
	err := ioutil.WriteFile("mutes.dc", mb.Bytes(), 0600)
	if err != nil {
		D("Error with writing out mute file:", err)
	}
}

func (m *Mutes) clean() {
	delcount := 0
	for userid, unmutetime := range m.users {
		if unmutetime.Before(time.Now().UTC()) {
			delete(m.users, userid)
			delcount++
		}
	}
}

func (m *Mutes) muteUserid(userid Userid, duration int64) {
	m.muteuserid <- &muteUserid{
		userid,
		time.Now().UTC().Add(time.Duration(duration)),
	}
}

func (m *Mutes) unmuteUserid(uid Userid) {
	m.unmuteuserid <- uid
}

func (m *Mutes) isUserMuted(conn *Connection) bool {
	if conn.user == nil {
		return true
	}
	muted := make(chan bool, 1)
	m.isuseridmuted <- &isUseridMuted{conn.user.id, muted}
	return <-muted
}
