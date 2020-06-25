package main

import (
	"testing"
	"time"
)

func TestMuteTimes(t *testing.T) {
	timeinfuture := time.Date(time.Now().Year()+1, time.September, 10, 23, 0, 0, 0, time.UTC)
	timeinpast := time.Date(time.Now().Year()-1, time.September, 10, 23, 0, 0, 0, time.UTC)
	uid := Userid(1)
	c := new(Connection)
	c.user = &User{}
	c.user.id = uid

	state.mutes[uid] = timeinfuture
	if !mutes.isUserMuted(c) {
		t.Error("user should be banned because the expiretime is in the future")
	}
	state.mutes[uid] = timeinpast
	if mutes.isUserMuted(c) {
		t.Error("user should NOT be banned because the expiretime is in the past")
	}

	mutes.clean()
	if len(state.mutes) > 0 {
		t.Error("mutes.clean did not clean the users")
	}
}
