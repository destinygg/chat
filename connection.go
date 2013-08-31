package main

import (
	"bytes"
	"code.google.com/p/go.net/websocket"
	"crypto/md5"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// regexp to detect three or more consecutive characters intended to be combined
// with another char (like accents, diacritics), if there are more than 5
// its most likely a zalgo pattern
// we also do not allow unicode non-breaking space/page/paragraph separators
// for an explanation on the unicode char classes used see:
// https://code.google.com/p/re2/wiki/Syntax
// cannot use the Z (separator) or Zs (space separator) because most of those
// are legitimate, we do not want non-breaking space characters tho
// http://www.fileformat.info/info/unicode/char/202f/index.htm
// http://www.fileformat.info/info/unicode/char/00a0/index.htm
var invalidmessage = regexp.MustCompile(`\p{M}{5,}|[\p{Zl}\p{Zp}\x{202f}\x{00a0}]`)

type Connection struct {
	socket         *websocket.Conn
	send           chan *message
	sendmarshalled chan *message
	blocksend      chan *message
	banned         chan bool
	stop           chan bool
	user           *User
	lastactive     time.Time
	ping           chan time.Time
	sync.RWMutex
}

type SimplifiedUser struct {
	Nick     string    `json:"nick"`
	Features *[]string `json:"features,omitempty"`
}

type EventDataIn struct {
	Data      string `json:"data"`
	Extradata string `json:"extradata"`
	Duration  int64  `json:"duration"`
}

type EventDataOut struct {
	*SimplifiedUser
	Targetuserid Userid `json:"-"`
	Timestamp    int64  `json:"timestamp"`
	Data         string `json:"data,omitempty"`
	Extradata    string `json:"extradata,omitempty"`
}

type BanIn struct {
	Nick        string `json:"nick"`
	BanIP       bool   `json:"banip"`
	Duration    int64  `json:"duration"`
	Ispermanent bool   `json:"ispermanent"`
	Reason      string `json:"reason"`
}

type PingOut struct {
	Timestamp int64 `json:"data"`
}

type message struct {
	event string
	data  interface{}
}

// Create a new connection using the specified socket and router.
func newConnection(s *websocket.Conn, user *User) {
	c := &Connection{
		socket:         s,
		send:           make(chan *message, SENDCHANNELSIZE),
		sendmarshalled: make(chan *message, SENDCHANNELSIZE),
		blocksend:      make(chan *message),
		banned:         make(chan bool, 8),
		stop:           make(chan bool),
		user:           user,
		lastactive:     time.Now(),
		ping:           make(chan time.Time, 2),
		RWMutex:        sync.RWMutex{},
	}

	go c.writePumpText()
	c.readPumpText()
}

func (c *Connection) readPumpText() {
	defer func() {
		namescache.disconnect(c.user)
		c.Quit()
		c.socket.Close()
	}()

	if c.user != nil {
		c.rlockUserIfExists()
		if c.user.connections > 5 {
			c.runlockUserIfExists()
			c.SendError("toomanyconnections")
			c.stop <- true
			return
		}
		c.runlockUserIfExists()
	} else {
		namescache.addConnection()
	}

	hub.register <- c
	c.Names()
	c.Join() // broadcast to the chat that a user has connected

	message := make([]byte, MAXMESSAGESIZE)
	for {
		// need to rearm the deadline on every read, or else the lib disconnects us
		// same thing for the writeDeadline
		c.socket.SetReadDeadline(time.Now().Add(READTIMEOUT))
		n, err := c.socket.Read(message)
		if err != nil {
			break
		}

		name, data, err := Unpack(string(message[:n]))
		if err != nil {
			// invalid protocol message from the client, just ignore it,
			// disconnect the user
			return
		}

		// update for timeout, need lock because we check it in the write goroutine
		c.Lock()
		c.lastactive = time.Now()
		c.Unlock()

		// dispatch
		switch name {
		case "MSG":
			c.OnMsg(data)
		case "PRIVMSG":
			c.OnPrivmsg(data)
		case "MUTE":
			c.OnMute(data)
		case "UNMUTE":
			c.OnUnmute(data)
		case "BAN":
			c.OnBan(data)
		case "UNBAN":
			c.OnUnban(data)
		case "SUBONLY":
			c.OnSubonly(data)
		case "PING":
			c.OnPing(data)
		case "PONG":
			c.OnPong(data)
		}
	}
}

func (c *Connection) writePumpText() {
	defer func() {
		hub.unregister <- c
		c.socket.Close() // Necessary to force reading to stop, will start the cleanup
	}()

	for {
		select {
		case t, ok := <-c.ping:
			if !ok {
				return
			}
			// doing it on the write goroutine because this one has a select
			c.RLock()
			interval := t.Sub(c.lastactive)
			c.RUnlock()
			if interval > PINGINTERVAL && interval < PINGTIMEOUT {
				c.Ping()
			} else if interval > PINGTIMEOUT {
				// disconnect user, stop goroutines
				return
			}
		case <-c.banned:
			websocket.Message.Send(c.socket, `ERR "banned"`)
			return
		case <-c.stop:
			return
		case m := <-c.blocksend:
			c.rlockUserIfExists()
			if data, err := Marshal(m.data); err == nil {
				c.runlockUserIfExists()
				if data, err := Pack(m.event, data); err == nil {
					if err := websocket.Message.Send(c.socket, string(data)); err != nil {
						return
					}
				}
			} else {
				c.runlockUserIfExists()
			}
		case m := <-c.send:
			c.rlockUserIfExists()
			if data, err := Marshal(m.data); err == nil {
				c.runlockUserIfExists()
				if data, err := Pack(m.event, data); err == nil {
					if err := websocket.Message.Send(c.socket, string(data)); err != nil {
						return
					}
				}
			} else {
				c.runlockUserIfExists()
			}
		case message := <-c.sendmarshalled:
			data := message.data.([]byte)
			if data, err := Pack(message.event, data); err == nil {
				if err := websocket.Message.Send(c.socket, string(data)); err != nil {
					return
				}
			}
		}
	}
}

func (c *Connection) rlockUserIfExists() {
	if c.user == nil {
		return
	}

	c.user.RLock()
}

func (c *Connection) runlockUserIfExists() {
	if c.user == nil {
		return
	}

	c.user.RUnlock()
}

func (c *Connection) Emit(event string, data interface{}) {
	c.send <- &message{
		event: event,
		data:  data,
	}
}

func (c *Connection) EmitBlock(event string, data interface{}) {
	c.blocksend <- &message{
		event: event,
		data:  data,
	}
	return
}

func (c *Connection) Broadcast(event string, data *EventDataOut) {
	c.rlockUserIfExists()
	marshalled, _ := Marshal(data)
	c.runlockUserIfExists()

	m := &message{
		event: event,
		data:  marshalled,
	}
	hub.broadcast <- m
	// by definition only users can send messages
	logEvent(c.user.id, event, data)
}

func (c *Connection) canModerateUser(nick string) (bool, Userid) {
	if c.user == nil || utf8.RuneCountInString(nick) == 0 {
		return false, 0
	}

	uid, protected := usertools.getUseridForNick(nick)
	if uid == 0 || c.user.id == uid || protected {
		return false, uid
	}

	return true, uid
}

func (c *Connection) getEventDataOut() *EventDataOut {
	out := new(EventDataOut)
	if c.user != nil {
		out.SimplifiedUser = c.user.simplified
	}
	out.Timestamp = unixMilliTime()
	return out
}

func (c *Connection) Join() {
	if c.user != nil {
		c.rlockUserIfExists()
		defer c.runlockUserIfExists()
		if c.user.connections == 1 {
			c.Broadcast("JOIN", c.getEventDataOut())
		}
	}
}

func (c *Connection) Quit() {
	if c.user != nil {
		c.rlockUserIfExists()
		defer c.runlockUserIfExists()
		if c.user.connections <= 0 {
			c.Broadcast("QUIT", c.getEventDataOut())
		}
	}
}

func (c *Connection) OnMsg(data []byte) {
	m := &EventDataIn{}
	if err := Unmarshal(data, m); err != nil {
		c.SendError("protocolerror")
		return
	}

	if c.user == nil {
		c.SendError("needlogin")
		return
	}

	msg := strings.TrimSpace(m.Data)
	msglen := utf8.RuneCountInString(msg)
	if !utf8.ValidString(msg) || msglen == 0 || msglen > 512 || invalidmessage.MatchString(msg) {
		c.SendError("invalidmsg")
		return
	}

	if mutes.isUserMuted(c) {
		c.SendError("muted")
		return
	}

	if !hub.canUserSpeak(c) {
		c.SendError("submode")
		return
	}

	if c.user != nil && !c.user.isBot() {

		// very simple heuristics of "punishing" the flooding user
		// if the user keeps spamming, the delay between messages increases
		// this delay resets after a fixed amount of time
		now := time.Now()
		difference := now.Sub(c.user.lastmessagetime)
		switch {
		case difference <= DELAY:
			c.user.delayscale *= 2
		case difference > MAXTHROTTLETIME:
			c.user.delayscale = 1
		}
		sendtime := c.user.lastmessagetime.Add(time.Duration(c.user.delayscale) * DELAY)
		if sendtime.After(now) {
			c.SendError("throttled")
			return
		}
		c.user.lastmessagetime = now

		hash := md5.New()
		h := hash.Sum([]byte(msg))
		if bytes.Equal(h, c.user.lastmessage) {
			c.user.delayscale++
			c.SendError("duplicate")
			return
		}
		c.user.lastmessage = h

	}

	out := c.getEventDataOut()
	out.Data = msg
	c.Broadcast("MSG", out)
}

func (c *Connection) OnPrivmsg(data []byte) {
	/*
		if err := Unmarshal(data, &stuct{}); err != nil {
			return
		}
	*/
	// TODO check if valid utf8, api call? need to be sync so that we know what to return, not a problem, already running in a goroutine
}

func (c *Connection) Names() {
	c.sendmarshalled <- &message{
		"NAMES",
		namescache.getNames(),
	}
}

func (c *Connection) OnMute(data []byte) {
	mute := &EventDataIn{} // Data is the nick
	if err := Unmarshal(data, mute); err != nil {
		c.SendError("protocolerror")
		return
	}

	if c.user == nil || !c.user.isModerator() {
		c.SendError("nopermission")
		return
	}

	ok, uid := c.canModerateUser(mute.Data)
	if !ok || uid == 0 {
		c.SendError("nopermission")
		return
	}

	if mute.Duration == 0 {
		mute.Duration = int64(DEFAULTMUTEDURATION)
	}

	if time.Duration(mute.Duration) > 7*24*time.Hour {
		c.SendError("protocolerror") // too long mute
		return
	}

	mutes.muteUserid(uid, mute.Duration)
	out := c.getEventDataOut()
	out.Data = mute.Data
	out.Targetuserid = uid
	c.Broadcast("MUTE", out)
}

func (c *Connection) OnUnmute(data []byte) {
	user := &EventDataIn{} // Data is the nick
	if err := Unmarshal(data, user); err != nil || utf8.RuneCountInString(user.Data) == 0 {
		c.SendError("protocolerror")
		return
	}

	if c.user == nil || !c.user.isModerator() {
		c.SendError("nopermission")
		return
	}

	uid, _ := usertools.getUseridForNick(user.Data)
	if uid == 0 {
		c.SendError("notfound")
		return
	}

	mutes.unmuteUserid(uid)
	out := c.getEventDataOut()
	out.Data = user.Data
	out.Targetuserid = uid
	c.Broadcast("UNMUTE", out)
}

func (c *Connection) Muted() {
}

func (c *Connection) OnBan(data []byte) {
	ban := &BanIn{}
	if err := Unmarshal(data, ban); err != nil {
		c.SendError("protocolerror")
		return
	}

	if c.user == nil {
		c.SendError("nopermission")
		return
	}

	if !c.user.isModerator() {
		c.SendError("nopermission")
		return
	}

	ok, uid := c.canModerateUser(ban.Nick)
	if uid == 0 {
		c.SendError("notfound")
		return
	} else if !ok {
		c.SendError("nopermission")
		return
	}

	reason := strings.TrimSpace(ban.Reason)
	if utf8.RuneCountInString(reason) == 0 || !utf8.ValidString(reason) {
		c.SendError("needbanreason")
		return
	}

	if ban.Duration == 0 {
		ban.Duration = int64(DEFAULTBANDURATION)
	}

	bans.banUser(c.user.id, uid, ban)

	out := c.getEventDataOut()
	out.Data = ban.Nick
	out.Targetuserid = uid
	c.Broadcast("BAN", out)
}

func (c *Connection) OnUnban(data []byte) {
	user := &EventDataIn{}
	if err := Unmarshal(data, user); err != nil {
		c.SendError("protocolerror")
		return
	}

	if c.user == nil || !c.user.isModerator() {
		c.SendError("nopermission")
		return
	}

	uid, _ := usertools.getUseridForNick(user.Data)
	if uid == 0 {
		c.SendError("notfound")
		return
	}

	bans.unbanUserid(uid)
	out := c.getEventDataOut()
	out.Data = user.Data
	out.Targetuserid = uid
	c.Broadcast("UNBAN", out)
}

func (c *Connection) Banned() {
	c.banned <- true
}

func (c *Connection) OnSubonly(data []byte) {
	m := &EventDataIn{} // Data is on/off
	if err := Unmarshal(data, m); err != nil {
		c.SendError("protocolerror")
		return
	}

	if c.user == nil || !c.user.isModerator() {
		c.SendError("nopermission")
		return
	}

	switch {
	case m.Data == "on":
		hub.toggleSubmode(true)
	case m.Data == "off":
		hub.toggleSubmode(false)
	default:
		c.SendError("protocolerror")
		return
	}

	out := c.getEventDataOut()
	out.Data = m.Data
	c.Broadcast("SUBONLY", out)
}

func (c *Connection) Ping() {
	d := &PingOut{
		time.Now().UnixNano(),
	}

	c.Emit("PING", d)
}

func (c *Connection) OnPing(data []byte) {
	c.Emit("PONG", data)
}

func (c *Connection) OnPong(data []byte) {
}

func (c *Connection) SendError(identifier string) {
	c.EmitBlock("ERR", identifier)
}

func (c *Connection) Refresh() {
	c.EmitBlock("REFRESH", c.getEventDataOut())
	c.stop <- true
}
