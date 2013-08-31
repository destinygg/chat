package main

import (
	"bytes"
	"encoding/gob"
	"io/ioutil"
	"sync"
	"time"
)

type Mutes struct {
	users map[Userid]time.Time
	sync.RWMutex
}

var (
	mutes = Mutes{
		make(map[Userid]time.Time),
		sync.RWMutex{},
	}
)

func initMutes() {
	mutes.load(false)
}

func (m *Mutes) load(skiplock bool) {
	if !skiplock {
		m.Lock()
		defer m.Unlock()
	}

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

func (m *Mutes) save(skiplock bool) {
	if !skiplock {
		m.RLock()
		defer m.RUnlock()
	}

	mb := new(bytes.Buffer)
	enc := gob.NewEncoder(mb)
	enc.Encode(&m.users)
	err := ioutil.WriteFile("mutes.dc", mb.Bytes(), 0600)
	if err != nil {
		D("Error with writing out mute file:", err)
	}
}

func (m *Mutes) clean() {
	m.Lock()
	defer m.Unlock()

	for uid, unmutetime := range m.users {
		if isExpiredUTC(unmutetime) {
			delete(m.users, uid)
		}
	}
	m.save(true)
}

func (m *Mutes) muteUserid(uid Userid, duration int64) {
	m.Lock()
	defer m.Unlock()

	m.users[uid] = time.Now().UTC().Add(time.Duration(duration))
	m.save(true)
}

func (m *Mutes) unmuteUserid(uid Userid) {
	m.Lock()
	defer m.Unlock()

	delete(m.users, uid)
	m.save(true)
}

func (m *Mutes) isUserMuted(c *Connection) bool {
	if c.user == nil {
		return true
	}

	m.RLock()
	defer m.RUnlock()

	t, ok := m.users[c.user.id]
	if !ok {
		return false
	}
	return !isExpiredUTC(t)
}
