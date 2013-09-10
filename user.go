package main

import (
	"encoding/json"
	"fmt"
	"github.com/vmihailenco/redis"
	"io/ioutil"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type userTools struct {
	nicklookup  map[string]*uidprot
	nicklock    sync.RWMutex
	featurelock sync.RWMutex
	features    map[uint32][]string
}

var (
	cookievalid = regexp.MustCompile("^[a-z0-9]{32}$")
	usertools   = userTools{
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

type sessionUser struct {
	Username string
	UserId   string
	Features []string
}

func initUsers(redisdb int64) {
	usertools.loadUserids()
	go usertools.setupRefreshUser(redisdb)
}

func (ut *userTools) loadUserids() {
	ut.nicklock.Lock()
	defer ut.nicklock.Unlock()

	getUsers(func(uid Userid, nick string, protected bool) {
		ut.nicklookup[strings.ToLower(nick)] = &uidprot{uid, protected}
	})

	D("Loaded", len(ut.nicklookup), "nicks")
}

func (ut *userTools) getUseridForNick(nick string) (Userid, bool) {
	ut.nicklock.RLock()
	defer ut.nicklock.RUnlock()
	d, ok := ut.nicklookup[strings.ToLower(nick)]
	if !ok {
		return 0, false
	}
	return d.id, d.protected
}

func (ut *userTools) addUser(u *User) {
	ut.nicklock.Lock()
	defer ut.nicklock.Unlock()
	ut.nicklookup[strings.ToLower(u.nick)] = &uidprot{u.id, u.isProtected()}
}

func (ut *userTools) setupRefreshUser(redisdb int64) {
	refreshuser := ut.getRefreshUserChan(redisdb)
	t := time.NewTicker(time.Minute)
	cp := watchdog.register("refreshuser thread", time.Minute)
	defer watchdog.unregister("refreshuser thread")

	for {
		select {
		case <-t.C:
			cp <- true // send heartbeat
		case msg := <-refreshuser:
			if msg.Err != nil {
				D("Error receiving from redis pub/sub channel refreshuser")
				refreshuser = ut.getRefreshUserChan(redisdb)
				continue
			}
			if len(msg.Message) == 0 { // a spurious message, ignore
				continue
			}

			user := userfromSession([]byte(msg.Message))
			namescache.refresh(user)
			hub.refreshuser <- user.id
		}
	}
}

func (ut *userTools) getRefreshUserChan(redisdb int64) chan *redis.Message {
refreshuseragain:
	c, err := rds.PubSubClient()
	if err != nil {
		B("Unable to create redis pubsub client: ", err)
		time.Sleep(500 * time.Millisecond)
		goto refreshuseragain
	}
	refreshuser, err := c.Subscribe(fmt.Sprintf("refreshuser-%d", redisdb))
	if err != nil {
		B("Unable to subscribe to the redis refreshuser channel: ", err)
		time.Sleep(500 * time.Millisecond)
		goto refreshuseragain
	}

	return refreshuser
}

// ----------
type Userid int32
type User struct {
	id              Userid
	nick            string
	features        uint32
	lastmessage     []byte
	lastmessagetime time.Time
	lastactive      time.Time
	delayscale      uint8
	simplified      *SimplifiedUser
	connections     int32
	sync.RWMutex
}

func userfromSession(m []byte) (u *User) {
	var su sessionUser
	err := json.Unmarshal(m, &su)
	if err != nil {
		D("Unable to unmarshal sessionuser string: ", string(m))
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
		lastactive:      time.Time{},
		delayscale:      1,
		simplified:      nil,
		connections:     0,
		RWMutex:         sync.RWMutex{},
	}

	u.setFeatures(su.Features)
	u.assembleSimplifiedUser()
	usertools.addUser(u)
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
	ip := r.RemoteAddr[:strings.LastIndex(r.RemoteAddr, ":")]
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

		sess := rds.Get(fmt.Sprintf("CHAT:session-%v", sessionid.Value))
		if sess.Err() != nil {
			return
		}
		authdata = []byte(sess.Val())
	} else {
		// try authtoken auth
		authtoken, err := r.Cookie("authtoken")
		if err != nil || !cookievalid.MatchString(authtoken.Value) {
			return
		}

		resp, err := http.PostForm(authtokenurl, url.Values{"authtoken": {authtoken.Value}})
		if resp.Body != nil {
			defer resp.Body.Close()
		}

		if err != nil || resp.StatusCode != 200 {
			return
		}

		authdata, _ = ioutil.ReadAll(resp.Body)
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
