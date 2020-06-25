package main

import (
	"testing"
	"time"
)

func TestBanTimes(t *testing.T) {
	timeinfuture := time.Date(time.Now().Year()+1, time.September, 10, 23, 0, 0, 0, time.UTC)
	timeinpast := time.Date(time.Now().Year()-1, time.September, 10, 23, 0, 0, 0, time.UTC)
	uid := Userid(1)
	ip := "10.1.2.3"

	bans.users[uid] = timeinfuture
	if !bans.isUseridBanned(uid) {
		t.Error("user should be banned because the expiretime is in the future")
	}
	bans.users[uid] = timeinpast
	if bans.isUseridBanned(uid) {
		t.Error("user should NOT be banned because the expiretime is in the past")
	}

	bans.ips[ip] = timeinfuture
	if !bans.isIPBanned(ip) {
		t.Error("ip should be banned because the expiretime is in the future")
	}
	bans.ips[ip] = timeinpast
	if bans.isIPBanned(ip) {
		t.Error("ip should NOT be banned because the expiretime is in the past")
	}

	bans.clean()
	if len(bans.users) > 0 {
		t.Error("bans.clean did not clean the users")
	}
	if len(bans.ips) > 0 {
		t.Error("bans.clean did not clean the ips")
	}
}
