//go:generate ffjson user.go
package main

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tideland/godm/v3/redis"
)

// ffjson: skip
type userTools struct {
	nicklookup  map[string]*uidprot
	nicklock    sync.RWMutex
	featurelock sync.RWMutex
	features    map[uint32][]string
}

var (
	usertools = userTools{
		nicklookup:  make(map[string]*uidprot),
		nicklock:    sync.RWMutex{},
		featurelock: sync.RWMutex{},
		features:    make(map[uint32][]string),
	}
)

const (
	ISADMIN      = 1 << iota
	ISMODERATOR  = 1 << iota
	ISVIP        = 1 << iota
	ISPROTECTED  = 1 << iota
	ISSUBSCRIBER = 1 << iota
	ISBOT        = 1 << iota
)

// ffjson: skip
type uidprot struct {
	id        Userid
	protected bool
}

func initUsers(redisdb int64) {
	go runRefreshUser(redisdb)
}

func (ut *userTools) getUseridForNick(nick string) (Userid, bool) {
	ut.nicklock.RLock()
	d, ok := ut.nicklookup[strings.ToLower(nick)]
	if !ok {
		uid, protected := db.getUser(nick)
		if uid != 0 {
			ut.nicklock.RUnlock()
			ut.nicklock.Lock()
			ut.nicklookup[strings.ToLower(nick)] = &uidprot{uid, protected}
			ut.nicklock.Unlock()
			return uid, protected
		}
		ut.nicklock.RUnlock()
		return 0, false
	}
	ut.nicklock.RUnlock()
	return d.id, d.protected
}

func (ut *userTools) addUser(u *User, force bool) {
	lowernick := strings.ToLower(u.nick)
	if !force {
		ut.nicklock.RLock()
		_, ok := ut.nicklookup[lowernick]
		ut.nicklock.RUnlock()
		if ok {
			return
		}
	}
	ut.nicklock.Lock()
	defer ut.nicklock.Unlock()
	ut.nicklookup[lowernick] = &uidprot{u.id, u.isProtected()}
}

func runRefreshUser(redisdb int64) {
	setupRedisSubscription("refreshuser", redisdb, func(result *redis.PublishedValue) {
		user := userfromSession(result.Value.Bytes())
		namescache.refresh(user)
		hub.refreshuser <- user.id
	})
}

// ----------
// ffjson: skip
type Userid int32

// ffjson: skip
type User struct {
	id              Userid
	nick            string
	features        uint32
	lastmessage     []byte
	lastmessagetime time.Time
	delayscale      uint8
	simplified      *SimplifiedUser
	connections     int32
	sync.RWMutex
}

type sessionuser struct {
	Username string   `json:"username"`
	UserId   string   `json:"userId"`
	Features []string `json:"features"`
}

func userfromSession(m []byte) (u *User) {
	var su sessionuser

	err := su.UnmarshalJSON(m)
	if err != nil {
		B("Unable to unmarshal sessionuser string: ", string(m))
		return
	}

	uid, err := strconv.ParseInt(su.UserId, 10, 32)
	if err != nil {
		return
	}

	u = &User{
		id:              Userid(uid),
		nick:            su.Username,
		features:        0,
		lastmessage:     nil,
		lastmessagetime: time.Time{},
		delayscale:      1,
		simplified:      nil,
		connections:     0,
		RWMutex:         sync.RWMutex{},
	}

	u.setFeatures(su.Features)

	forceupdate := false
	if cu := namescache.get(u.id); cu != nil && cu.features == u.features {
		forceupdate = true
	}

	u.assembleSimplifiedUser()
	usertools.addUser(u, forceupdate)
	return
}

func (u *User) featureGet(bitnum uint32) bool {
	return ((u.features & bitnum) != 0)
}

func (u *User) featureSet(bitnum uint32) {
	u.features |= bitnum
}

func (u *User) featureCount() (c uint8) {
	// Counting bits set, Brian Kernighan's way
	v := u.features
	for c = 0; v != 0; c++ {
		v &= v - 1 // clear the least significant bit set
	}
	return
}

// isModerator checks if the user can use mod commands
func (u *User) isModerator() bool {
	return u.featureGet(ISMODERATOR | ISADMIN | ISBOT)
}

// isSubscriber checks if the user can speak when the chat is in submode
func (u *User) isSubscriber() bool {
	return u.featureGet(ISSUBSCRIBER | ISADMIN | ISMODERATOR | ISVIP | ISBOT)
}

// isBot checks if the user is exempt from ratelimiting
func (u *User) isBot() bool {
	return u.featureGet(ISBOT)
}

// isProtected checks if the user can be moderated or not
func (u *User) isProtected() bool {
	return u.featureGet(ISADMIN | ISPROTECTED)
}

func (u *User) setFeatures(features []string) {
	for _, feature := range features {
		switch feature {
		case "admin":
			u.featureSet(ISADMIN)
		case "moderator":
			u.featureSet(ISMODERATOR)
		case "protected":
			u.featureSet(ISPROTECTED)
		case "subscriber":
			u.featureSet(ISSUBSCRIBER)
		case "vip":
			u.featureSet(ISVIP)
		case "bot":
			u.featureSet(ISBOT)
		default:
			if feature[:5] == "flair" {
				flair, err := strconv.Atoi(feature[5:])
				if err != nil {
					D("Could not parse unknown feature:", feature, err)
					continue
				}
				// six proper features, all others are just useless flairs
				u.featureSet(1 << (6 + uint8(flair)))
			}
		}
	}
}

func (u *User) assembleSimplifiedUser() {
	usertools.featurelock.RLock()
	f, ok := usertools.features[u.features]
	usertools.featurelock.RUnlock()

	if !ok {
		usertools.featurelock.Lock()
		defer usertools.featurelock.Unlock()

		numfeatures := u.featureCount()
		f = make([]string, 0, numfeatures)
		if u.featureGet(ISPROTECTED) {
			f = append(f, "protected")
		}
		if u.featureGet(ISSUBSCRIBER) {
			f = append(f, "subscriber")
		}
		if u.featureGet(ISVIP) {
			f = append(f, "vip")
		}
		if u.featureGet(ISMODERATOR) {
			f = append(f, "moderator")
		}
		if u.featureGet(ISADMIN) {
			f = append(f, "admin")
		}
		if u.featureGet(ISBOT) {
			f = append(f, "bot")
		}

		for i := uint8(6); i <= 26; i++ {
			if u.featureGet(1 << i) {
				flair := fmt.Sprintf("flair%d", i-6)
				f = append(f, flair)
			}
		}

		usertools.features[u.features] = f
	}

	u.simplified = &SimplifiedUser{
		u.nick,
		&f,
	}
}

// ----------
func getUserFromWebRequest(r *http.Request) (user *User, banned bool) {
	ip := r.Header.Get("X-Real-Ip")
	if ip == "" {
		ip, _, _ = net.SplitHostPort(r.RemoteAddr)
	}

	banned = bans.isIPBanned(ip)
	if banned {
		return
	}

	var authdata []byte
	// set up the user here from redis if the user has a session cookie
	sessionid, err := r.Cookie("sid")
	if err == nil {
		if !cookievalid.MatchString(sessionid.Value) {
			return
		}

		authdata, err = redisGetBytes(fmt.Sprintf("CHAT:session-%v", sessionid.Value))
		if err != nil || len(authdata) == 0 {
			return
		}
	} else {
		// try authtoken auth
		authtoken, err := r.Cookie("authtoken")
		if err != nil {
			return
		}

		authdata, err = api.getUserFromAuthToken(authtoken.Value)
		if err != nil {
			D("getUserFromAuthToken error", err)
			return
		}
	}

	user = userfromSession(authdata)
	if user == nil {
		return
	}

	banned = bans.isUseridBanned(user.id)
	if banned {
		return
	}

	cacheIPForUser(user.id, ip)
	// there is only ever one single "user" struct, the namescache makes sure of that
	user = namescache.add(user)
	return
}
