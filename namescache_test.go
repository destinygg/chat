package main

import (
	"sync"
	"testing"
)

func TestNamescacheRefresh(t *testing.T) {
	uid := Userid(1)

	u := &User{}
	u.id = uid
	u.nick = "testnick"
	u.setFeatures([]string{"admin", "moderator", "protected", "subscriber", "vip", "bot"})
	u.assembleSimplifiedUser()

	nc := &namesCache{
		users:   make(map[Userid]*User),
		RWMutex: sync.RWMutex{},
	}

	nc.add(u)

	if nu, ok := nc.users[uid]; ok {
		if nu.connections != 1 {
			t.Errorf("Usercount was not 1 but %v, %+v", u.connections, u)
		}
		if len(*nu.simplified.Features) != 6 {
			t.Errorf("Simplified user features length was not 6 %+v", nu.simplified.Features)
		}
	} else {
		t.Errorf("Namescache did not have user %+v", nu)
	}

	u = &User{}
	u.id = uid
	u.nick = "NEWNICK"
	u.setFeatures([]string{"protected"})
	u.assembleSimplifiedUser()

	nc.refresh(u)

	if nu, ok := nc.users[uid]; ok {
		if nu.nick != "NEWNICK" {
			t.Errorf("Users refresh did not succeed, nick was %+v", nu.nick)
		}
		if len(*nu.simplified.Features) != 1 {
			t.Errorf("Simplified user features length was not 1 %+v", nu.simplified.Features)
		}
	} else {
		t.Errorf("Namescache did not have user %+v", nu)
	}

}
