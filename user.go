package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	nicklookup    = make(map[string]uidprot)
	addnickuid    = make(chan *nickuidprot)
	getuidfornick = make(chan *nickchan, 256)
	cookievalid   = regexp.MustCompile("^[a-z0-9]{32}$")
	features      = make(map[uint32][]string)
	featurelock   = sync.RWMutex{}
)

const (
	ISADMIN      = 1 << iota
	ISMODERATOR  = 1 << iota
	ISVIP        = 1 << iota
	ISPROTECTED  = 1 << iota
	ISSUBSCRIBER = 1 << iota
	ISBOT        = 1 << iota
)

type BitField struct {
	data uint32
}

func (b *BitField) Get(bitnum uint32) bool {
	return ((b.data & bitnum) != 0)
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

type Userid int32
type User struct {
	id              Userid
	nick            string
	features        BitField
	lastmessage     []byte
	lastmessagetime time.Time
	lastactive      time.Time
	delayscale      uint8
	simplified      *SimplifiedUser
	connections     []*Connection
	sync.RWMutex
}

// isModerator checks if the user can use mod commands
func (u *User) isModerator() bool {
	return u.features.Get(ISMODERATOR | ISADMIN | ISBOT)
}

// isSubscriber checks if the user can speak when the chat is in submode
func (u *User) isSubscriber() bool {
	return u.features.Get(ISSUBSCRIBER | ISADMIN | ISMODERATOR | ISVIP)
}

// isBot checks if the user is exempt from ratelimiting
func (u *User) isBot() bool {
	return u.features.Get(ISBOT)
}

// isProtected checks if the user can be moderated or not
func (u *User) isProtected() bool {
	return u.features.Get(ISADMIN | ISPROTECTED)
}

func (u *User) setFeatures(features []string) {
	for _, feature := range features {
		switch feature {
		case "admin":
			u.features.Set(ISADMIN)
		case "moderator":
			u.features.Set(ISMODERATOR)
		case "protected":
			u.features.Set(ISPROTECTED)
		case "subscriber":
			u.features.Set(ISSUBSCRIBER)
		case "vip":
			u.features.Set(ISVIP)
		case "bot":
			u.features.Set(ISBOT)
		default:
			if feature[:5] == "flair" {
				flair, err := strconv.Atoi(feature[5:])
				if err != nil {
					D("Could not parse unknown feature:", feature, err)
					continue
				}
				// six proper features, all others are just useless flairs
				u.features.Set(1 << (6 + uint8(flair)))
			}
		}
	}
}

func (u *User) assembleSimplifiedUser() {
	featurelock.RLock()
	f, ok := features[u.features.data]
	featurelock.RUnlock()

	if !ok {
		featurelock.Lock()
		defer featurelock.Unlock()
		numfeatures := u.features.NumSet()
		f = make([]string, 0, numfeatures)
		if u.features.Get(ISPROTECTED) {
			f = append(f, "protected")
		}
		if u.features.Get(ISSUBSCRIBER) {
			f = append(f, "subscriber")
		}
		if u.features.Get(ISVIP) {
			f = append(f, "vip")
		}
		if u.features.Get(ISMODERATOR) {
			f = append(f, "moderator")
		}
		if u.features.Get(ISADMIN) {
			f = append(f, "admin")
		}
		if u.features.Get(ISBOT) {
			f = append(f, "bot")
		}

		for i := uint8(7); i < numfeatures; i++ {
			flair := fmt.Sprintf("flair%d", i-6)
			f = append(f, flair)
		}

		features[u.features.data] = f
	}

	u.simplified = &SimplifiedUser{
		u.nick,
		&f,
		1,
	}
}

type uidnickfeaturechan struct {
	userid   Userid
	nick     string
	features []string
	c        chan *User
}

type uidprot struct {
	id        Userid
	protected bool
}

type nickuidprot struct {
	nick string
	uidprot
}

type nickchan struct {
	nick string
	c    chan uidprot
}

type sessionUser struct {
	Username string
	UserId   string
	Features []string
}

func initUsers() {

	// goroutine for nick<->userid lookup without locks
	// important detail: all the nicks get normalized to their lowercase form
	// so that the case does not matter
	go (func() {
		loadUserids()
		for {
			select {
			case nu := <-addnickuid:
				nicklookup[nu.nick] = uidprot{nu.id, nu.protected}
			case nc := <-getuidfornick:
				select {
				case nc.c <- nicklookup[nc.nick]:
				default:
				}
			}
		}
	})()

	go (func() {

	restartpubsub:
		// goroutine to watch for messages that signal that a users data was modified
		psc, err := rds.PubSubClient()
		if err != nil {
			B("Unable to create redis pubsub client: ", err)
		}
		refreshuser, err := psc.Subscribe("refreshuser")
		if err != nil {
			B("Unable to subscribe to the redis refreshuser channel: ", err)
		}

		for msg := range refreshuser {
			if len(msg.Message) == 0 { // wtf, a spurious message
				continue
			}

			D("got refreshuser message: ", msg.Message)

			var su sessionUser
			err = json.Unmarshal([]byte(msg.Message), &su)
			if err != nil {
				D("Unable to unmarshal sessionuser string: ", msg.Message)
				continue
			}

			var protected bool
			for _, feature := range su.Features {
				if feature == "protected" {
					protected = true
					break
				}
			}

			uid, err := strconv.ParseInt(su.UserId, 10, 32)
			if err != nil {
				continue
			}

			addnickuid <- &nickuidprot{strings.ToLower(su.Username), uidprot{Userid(uid), protected}}
			hub.refreshuser <- Userid(uid)
		}
		D("REDIS PUBSUB CHANNEL CLOSED")
		goto restartpubsub
	})()

}

func loadUserids() {
	rows, err := db.Query(`
		SELECT DISTINCT
			u.userId,
			u.username,
			IF(IFNULL(f.featureId, 0) >= 1, 1, 0) AS protected
		FROM dfl_users AS u
		LEFT JOIN dfl_users_features AS f ON (
			f.userId = u.userId AND
			featureId = (SELECT featureId FROM dfl_features WHERE featureName = "protected")
		)
	`)

	if err != nil {
		B("Unable to load userids:", err)
		return
	}

	for rows.Next() {
		var uid Userid
		var nick string
		var protected bool

		err = rows.Scan(&uid, &nick, &protected)
		if err != nil {
			B("Unable to scan row: ", err)
			continue
		}

		nicklookup[strings.ToLower(nick)] = uidprot{uid, protected}

	}
	D("Loaded", len(nicklookup), "nicks")

}

func getUseridForNick(nick string) (Userid, bool) {
	c := make(chan uidprot)
	getuidfornick <- &nickchan{strings.ToLower(nick), c}
	d := <-c
	return d.id, d.protected
}

func getUser(r *http.Request) (u *User, banned bool) {

	pos := strings.LastIndex(r.RemoteAddr, ":")
	ip := r.RemoteAddr[:pos]

	// set up the user here from redis if the user has a session cookie
	sessionid, err := r.Cookie("sid")
	if err != nil || !cookievalid.MatchString(sessionid.Value) {
		banned = isUseridIPBanned(ip, 0)
		return
	}

	sess := rds.Get(fmt.Sprintf("CHAT:%v", sessionid.Value))
	if sess.Err() != nil {
		banned = isUseridIPBanned(ip, 0)
		return
	}

	var su sessionUser
	err = json.Unmarshal([]byte(sess.Val()), &su)
	if err != nil {
		B("Unable to unmarshal string: ", sess.Val())
	}

	id, err := strconv.ParseInt(su.UserId, 10, 32)
	if err != nil {
		return
	}
	userid := Userid(id)

	banned = isUseridIPBanned(ip, userid)
	if banned {
		return
	}

	uc := make(chan *User, 1)
	hub.getuser <- &uidnickfeaturechan{userid, su.Username, su.Features, uc}
	u = <-uc

	//D("User connected with id:", u.id, "and nick:", u.nick, "features:", u.simplified.Features, ip)
	return
}
