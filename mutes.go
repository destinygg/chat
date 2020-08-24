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

func (m *Mutes) muteTimeLeft(c *Connection) time.Duration {
	if c.user == nil {
		return time.Duration(0)
	}

	state.Lock()
	defer state.Unlock()

	muteExpirationTime, ok := state.mutes[c.user.id]
	if !ok {
		return time.Duration(0)
	}

	timeLeft := time.Until(muteExpirationTime)
	return timeLeft
}