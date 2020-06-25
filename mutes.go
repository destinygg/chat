package main

import (
	"time"
)

type Mutes int

var mutes Mutes

func (m *Mutes) clean() {
	state.Lock()
	defer state.Unlock()

	for uid, unmutetime := range state.mutes {
		if isExpiredUTC(unmutetime) {
			delete(state.mutes, uid)
		}
	}
	state.save()
}

func (m *Mutes) muteUserid(uid Userid, duration int64) {
	state.Lock()
	defer state.Unlock()

	state.mutes[uid] = time.Now().UTC().Add(time.Duration(duration))
	state.save()
}

func (m *Mutes) unmuteUserid(uid Userid) {
	state.Lock()
	defer state.Unlock()

	delete(state.mutes, uid)
	state.save()
}

func (m *Mutes) isUserMuted(c *Connection) bool {
	if c.user == nil {
		return true
	}

	state.Lock()
	defer state.Unlock()

	t, ok := state.mutes[c.user.id]
	if !ok {
		return false
	}
	return !isExpiredUTC(t)
}
