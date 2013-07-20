package main

import (
	"bytes"
	"encoding/gob"
	"io/ioutil"
	"time"
)

type useridMuted struct {
	userid Userid
	muted  chan bool
}

var (
	mutes        = make(map[Userid]time.Time)
	muteuserid   = make(chan *useridTime)
	useridmuted  = make(chan *useridMuted)
	unmuteuserid = make(chan Userid)
)

func initMutes() {
	go (func() {
		loadMutes()
		t := time.NewTimer(CLEANMUTESBANSPERIOD)
		for {
			select {
			case d := <-muteuserid:
				mutes[d.userid] = d.t
				saveMutes()
			case d := <-useridmuted:
				if t, ok := mutes[d.userid]; ok {
					d.muted <- t.After(time.Now().UTC())
				} else {
					d.muted <- false
				}
			case uid := <-unmuteuserid:
				delete(mutes, uid)
				saveMutes()
			case <-t.C:
				cleanMutes()
			}
		}
	})()
}

func loadMutes() {
	n, err := ioutil.ReadFile("mutes.dc")
	if err != nil {
		D("Error while reading from mutes file")
		return
	}
	m := bytes.NewBuffer(n)
	dec := gob.NewDecoder(m)
	err = dec.Decode(&mutes)
	if err != nil {
		D("Error decoding mutes file")
	}
}

func saveMutes() {
	m := new(bytes.Buffer)
	enc := gob.NewEncoder(m)
	enc.Encode(&mutes)
	err := ioutil.WriteFile("mutes.dc", m.Bytes(), 0600)
	if err != nil {
		D("Error with writing out mute file:", err)
	}
}

func cleanMutes() {
	delcount := 0
	for userid, unmutetime := range mutes {
		if unmutetime.Before(time.Now().UTC()) {
			delete(mutes, userid)
			delcount++
		}
	}
	D("Cleaned mutes, deleted records: ", delcount)
}

func muteUserid(userid Userid, duration int64) {
	muteuserid <- &useridTime{userid, time.Now().UTC().Add(time.Duration(duration))}
}

func unmuteUserid(uid Userid) {
	unmuteuserid <- uid
}

func isUserMuted(conn *Connection) bool {
	if conn.user == nil {
		return true
	}
	muted := make(chan bool, 1)
	useridmuted <- &useridMuted{conn.user.id, muted}
	return <-muted
}
