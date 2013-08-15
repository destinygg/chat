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
	nicklookup    map[string]uidprot
	adduser       chan *nickuidprot
	getuidfornick chan *nickchan
	featurelock   sync.RWMutex
	features      map[uint32][]string
}

var (
	cookievalid = regexp.MustCompile("^[a-z0-9]{32}$")
	usertools   = userTools{
		nicklookup:    make(map[string]uidprot),
		adduser:       make(chan *nickuidprot),
		getuidfornick: make(chan *nickchan, 256),
		featurelock:   sync.RWMutex{},
		features:      make(map[uint32][]string),
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

func initUsers(redisdb int64) {

	// goroutine for nick<->userid lookup without locks
	// important detail: all the nicks get normalized to their lowercase form
	// so that the case does not matter
	go usertools.setupUserLookup()
	go usertools.setupRefreshUser(redisdb)

}

func (ut *userTools) setupUserLookup() {
	ut.loadUserids()
	t := time.NewTicker(time.Minute)
	cp := watchdog.register("userid lookup thread", time.Minute)
	defer watchdog.unregister("userid lookup thread")

	for {
		select {
		case <-t.C:
			cp <- true
		case nu := <-ut.adduser:
			ut.nicklookup[strings.ToLower(nu.nick)] = uidprot{nu.id, nu.protected}
		case nc := <-ut.getuidfornick:
			select {
			case nc.c <- ut.nicklookup[strings.ToLower(nc.nick)]:
			default:
			}
		}
	}
}

func (ut *userTools) getUseridForNick(nick string) (Userid, bool) {
	c := make(chan uidprot, 1)
	ut.getuidfornick <- &nickchan{nick, c}
	d := <-c
	return d.id, d.protected
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
			if len(msg.Message) == 0 { // wtf, a spurious message
				continue
			}

			var su sessionUser
			err := json.Unmarshal([]byte(msg.Message), &su)
			if err != nil {
				D("Unable to unmarshal sessionuser string: ", msg.Message)
				continue
			}

			uid, err := strconv.ParseInt(su.UserId, 10, 32)
			if err != nil {
				continue
			}

			// names cache user refresh, not actually used anywhere
			user := &User{
				id:              Userid(uid),
				nick:            su.Username,
				features:        0,
				lastmessage:     nil,
				lastmessagetime: time.Time{},
				lastactive:      time.Time{},
				delayscale:      1,
				simplified:      nil,
				RWMutex:         sync.RWMutex{},
			}
			user.setFeatures(su.Features)
			user.assembleSimplifiedUser()
			namescache.refresh(user)

			usertools.adduser <- &nickuidprot{
				su.Username,
				uidprot{Userid(uid), user.isProtected()},
			}
			hub.refreshuser <- Userid(uid)
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

func (ut *userTools) loadUserids() {
	rows, err := db.Query(`
		SELECT DISTINCT
			u.userId,
			u.username,
			IF(IFNULL(f.featureId, 0) >= 1, 1, 0) AS protected
		FROM dfl_users AS u
		LEFT JOIN dfl_users_features AS f ON (
			f.userId = u.userId AND
			featureId = (SELECT featureId FROM dfl_features WHERE featureName IN("protected", "admin") LIMIT 1)
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

		ut.nicklookup[strings.ToLower(nick)] = uidprot{uid, protected}
	}

	D("Loaded", len(ut.nicklookup), "nicks")
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
	sync.RWMutex
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
	return u.featureGet(ISSUBSCRIBER | ISADMIN | ISMODERATOR | ISVIP)
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

		for i := uint8(6); i <= 28; i++ {
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
		1,
	}
}

// ----------
func getUser(r *http.Request) (user *User, banned bool) {

	pos := strings.LastIndex(r.RemoteAddr, ":")
	ip := r.RemoteAddr[:pos]
	var authdata []byte
	var su sessionUser

	// set up the user here from redis if the user has a session cookie
	sessionid, err := r.Cookie("sid")
	if err == nil {
		if !cookievalid.MatchString(sessionid.Value) {
			banned = bans.isUseridIPBanned(ip, 0)
			return
		}

		sess := rds.Get(fmt.Sprintf("CHAT:session-%v", sessionid.Value))
		if sess.Err() != nil {
			banned = bans.isUseridIPBanned(ip, 0)
			return
		}
		authdata = []byte(sess.Val())
	} else {
		// try authtoken auth
		authtoken, err := r.Cookie("authtoken")
		if err != nil || !cookievalid.MatchString(authtoken.Value) {
			banned = bans.isUseridIPBanned(ip, 0)
			return
		}

		resp, err := http.PostForm(authtokenurl, url.Values{"authtoken": {authtoken.Value}})
		if resp.Body != nil {
			defer resp.Body.Close()
		}

		if err != nil || resp.StatusCode != 200 {
			banned = bans.isUseridIPBanned(ip, 0)
			return
		}

		authdata, _ = ioutil.ReadAll(resp.Body)
	}

	err = json.Unmarshal(authdata, &su)
	if err != nil {
		D("Unable to unmarshal string: ", string(authdata))
		banned = bans.isUseridIPBanned(ip, 0)
		return
	}

	id, err := strconv.ParseInt(su.UserId, 10, 32)
	if err != nil {
		D("Unable to parse UserId", err, su.UserId)
		banned = bans.isUseridIPBanned(ip, 0)
		return
	}

	userid := Userid(id)
	banned = bans.isUseridIPBanned(ip, userid)
	if banned {
		return
	}

	cacheIPForUser(userid, ip)
	user = &User{
		id:              userid,
		nick:            su.Username,
		features:        0,
		lastmessage:     nil,
		lastmessagetime: time.Time{},
		lastactive:      time.Time{},
		delayscale:      1,
		simplified:      nil,
		RWMutex:         sync.RWMutex{},
	}
	user.setFeatures(su.Features)
	user.assembleSimplifiedUser()

	user = namescache.add(user)
	return
}
