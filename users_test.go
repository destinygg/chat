package main

import (
	"testing"
)

func TestUserLookup(t *testing.T) {
	uid := Userid(1)
	nick := "testnick"

	u := &User{}
	u.id = uid
	u.nick = nick

	usertools.addUser(u, true)
	if r, _ := usertools.getUseridForNick(nick); r != uid {
		t.Error("usertools.adduser failed, returned uid was: ", r, "(expected:", uid, ") for nick: ", nick)
	}

	nick = "TESTNICK"
	if r, _ := usertools.getUseridForNick(nick); r != uid {
		t.Error("usertools.adduser failed, returned uid was: ", r, "(expected:", uid, ") for nick: ", nick)
	}
}

func TestFeatures(t *testing.T) {
	uid := Userid(1)
	nick := "testnick"

	u := &User{}
	u.id = uid
	u.nick = nick

	if u.featureGet(ISPROTECTED) {
		t.Error("feature should not be set")
	}
	if u.featureGet(ISSUBSCRIBER) {
		t.Error("feature should not be set")
	}
	if u.featureGet(ISVIP) {
		t.Error("feature should not be set")
	}
	if u.featureGet(ISMODERATOR) {
		t.Error("feature should not be set")
	}
	if u.featureGet(ISADMIN) {
		t.Error("feature should not be set")
	}
	if u.featureGet(ISBOT) {
		t.Error("feature should not be set")
	}
	for i := uint8(6); i <= 28; i++ {
		if u.featureGet(1 << i) {
			t.Error("feature should not be set")
		}
	}
	if u.isProtected() {
		t.Error("should not be protected")
	}
	if u.isBot() {
		t.Error("should not be bot")
	}
	if u.isSubscriber() {
		t.Error("should not be subscriber")
	}
	if u.isModerator() {
		t.Error("should not be moderator")
	}

	//--------
	features := []string{"admin", "moderator", "protected", "subscriber", "vip", "bot"}
	u.setFeatures(features)

	if !u.featureGet(ISPROTECTED) {
		t.Error("feature should be set")
	}
	if !u.featureGet(ISSUBSCRIBER) {
		t.Error("feature should be set")
	}
	if !u.featureGet(ISVIP) {
		t.Error("feature should be set")
	}
	if !u.featureGet(ISMODERATOR) {
		t.Error("feature should be set")
	}
	if !u.featureGet(ISADMIN) {
		t.Error("feature should be set")
	}
	if !u.featureGet(ISBOT) {
		t.Error("feature should be set")
	}
	for i := uint8(6); i <= 28; i++ {
		if u.featureGet(1 << i) {
			t.Error("feature should not be set")
		}
	}
	if !u.isProtected() {
		t.Error("should be protected")
	}
	if !u.isBot() {
		t.Error("should be bot")
	}
	if !u.isSubscriber() {
		t.Error("should be subscriber")
	}
	if !u.isModerator() {
		t.Error("should be moderator")
	}
}
