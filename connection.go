package main

import (
	"bytes"
	"crypto/md5"
	"github.com/garyburd/go-websocket/websocket"
	"io/ioutil"
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
	socket     *websocket.Conn
	send       chan *message
	stop       chan bool
	user       *User
	lastactive time.Time
	ping       chan time.Time
	sync.RWMutex
}

type SimplifiedUser struct {
	Nick        string    `json:"nick"`
	Features    *[]string `json:"features,omitempty"`
	Connections uint8     `json:"connections,omitempty"`
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

type NamesOut struct {
	Users           *[]*SimplifiedUser `json:"users"`
	Connectioncount int                `json:"connectioncount"`
}

// Create a new connection using the specified socket and router.
func newConnection(s *websocket.Conn, user *User) {
	c := &Connection{
		socket:     s,
		send:       make(chan *message, SENDCHANNELSIZE),
		stop:       make(chan bool),
		user:       user,
		lastactive: time.Now(),
		ping:       make(chan time.Time, 2),
		RWMutex:    sync.RWMutex{},
	}

	if c.user != nil {
		c.user.assembleSimplifiedUser()
	}

	go c.writePumpText()
	c.readPumpText()

}

func (c *Connection) readPumpText() {
	defer func() {
		c.Quit()
		hub.unregister <- c
	}()

	hub.register <- c
	c.Names() // send the currently connected users to the client
	c.Join()  // broadcast to the chat that a user has connected
	c.socket.SetReadLimit(MAXMESSAGESIZE)

	for {
		// need to rearm the deadline on every read, or else the lib disconnects us
		// same thing for the writeDeadline
		c.socket.SetReadDeadline(time.Now().Add(READTIMEOUT))
		op, r, err := c.socket.NextReader()
		if err != nil {
			return
		}

		if op != websocket.OpText {
			continue
		}

		message, err := ioutil.ReadAll(r)
		if err != nil {
			D("read error: ", err)
			return
		}

		name, data, err := Unpack(message)
		if err != nil {
			// invalid protocol message from the client, just ignore it,
			// should we instead disconnect the user?
			continue
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
		c.write(websocket.OpClose, []byte{})
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
		case <-c.stop:
			return
		case message, ok := <-c.send:
			if !ok {
				return
			}
			if data, err := Marshal(message.data); err == nil {
				if data, err := Pack(message.event, data); err == nil {
					if err := c.write(websocket.OpText, data); err != nil {
						D("write error: ", err)
						return
					}
				}
			}
		}
	}
}

func (c *Connection) Emit(event string, data interface{}) {
	c.send <- &message{
		event: event,
		data:  data,
	}
}

func (c *Connection) Broadcast(event string, data *EventDataOut) {
	m := &message{
		event: event,
		data:  data,
	}
	hub.broadcast <- m
	// by definition only users can send messages
	logEvent(c.user.id, event, data)
}

// Helper for writing to socket with deadline.
func (c *Connection) write(opCode int, payload []byte) error {
	c.socket.SetWriteDeadline(time.Now().Add(WRITETIMEOUT))
	return c.socket.WriteMessage(opCode, payload)
}

func (c *Connection) getSimplifiedUser() *SimplifiedUser {
	if c.user == nil {
		return nil
	}

	return c.user.simplified
}

func (c *Connection) canModerateUser(nick string) (bool, Userid) {
	if c.user == nil || utf8.RuneCountInString(nick) == 0 {
		return false, 0
	}

	uid := <-getUseridForNick(nick)
	if uid == 0 || c.user.id == uid {
		return false, 0
	}

	hub.RLock()
	defer hub.RUnlock()
	user, ok := hub.users[uid]

	if !ok {
		return false, 0
	}

	for _, feature := range *user.Features {
		if feature == "protected" {
			return false, 0
		}
	}

	return true, uid
}

func (c *Connection) getEventDataOut() *EventDataOut {
	out := new(EventDataOut)
	out.SimplifiedUser = c.getSimplifiedUser()
	out.Timestamp = unixMilliTime()
	return out
}

func (c *Connection) Join() {
	if c.user != nil {
		c.Broadcast("JOIN", c.getEventDataOut())
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

	if isUserMuted(c) {
		c.SendError("muted")
		return
	}

	if !hub.canUserSpeak(c) {
		c.SendError("submode")
		return
	}

	if c.user != nil && !c.user.isBot() {

		// very simple heuristics of "punishing" the flooding user
		// if the user keeps spamming the delay between messages increases
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

	hub.RLock()

	// TODO think about how to cache this effectively and race-free, not ideal
	i := 0
	users := make([]*SimplifiedUser, len(hub.users))
	conncount := len(hub.connections)
	for _, v := range hub.users {
		users[i] = v
		i++
	}
	hub.RUnlock()

	c.Emit("NAMES", &NamesOut{&users, conncount})
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

	muteUserid(uid, mute.Duration)
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

	uid := <-getUseridForNick(user.Data)
	if uid == 0 {
		c.SendError("nopermission")
		return
	}

	unmuteUserid(uid)
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

	if c.user == nil || !c.user.isModerator() {
		c.SendError("nopermission")
		return
	}

	ok, uid := c.canModerateUser(ban.Nick)
	if !ok || uid == 0 {
		c.SendError("nopermission")
		return
	}

	if ban.Duration == 0 {
		ban.Duration = int64(DEFAULTBANDURATION)
	}

	if ban.Ispermanent {
		reason := strings.TrimSpace(ban.Reason)
		if utf8.RuneCountInString(reason) == 0 || !utf8.ValidString(reason) {
			c.SendError("needbanreason")
			return
		}
	}

	banUser(uid, ban)
	logBan(c.user.id, uid, ban, "")
	if ban.BanIP {
		ips := hub.getIPsForUserid(uid)
		for stringip, ip := range ips {
			hub.ipbans <- ip
			logBan(c.user.id, uid, ban, stringip)
		}
	}

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

	uid := <-getUseridForNick(user.Data)
	if uid == 0 {
		c.SendError("nopermission")
		return
	}

	unbanUserid(uid)
	out := c.getEventDataOut()
	out.Data = user.Data
	out.Targetuserid = uid
	c.Broadcast("UNBAN", out)
}

func (c *Connection) Banned() {
	c.SendError("banned")
	c.stop <- true
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
		hub.sublock.Lock()
		hub.submode = true
		hub.sublock.Unlock()
	case m.Data == "off":
		hub.sublock.Lock()
		hub.submode = false
		hub.sublock.Unlock()
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
	/*
		d := &PingOut{}
		if err := Unmarshal(data, d); err != nil {
			c.SendError("protocolerror")
			return
		}
		the lag would be time.Unix(d.timestamp / time.Second, d.timestamp % time.Second)
		we dont care because c.lastactive was already updated in the read side
	*/
}

func (c *Connection) Quit() {
	if c.user != nil {
		c.Broadcast("QUIT", c.getEventDataOut())
	}
}

func (c *Connection) SendError(identifier string) {
	c.Emit("ERR", identifier)
}

func (c *Connection) Refresh() {
	c.Emit("REFRESH", c.getEventDataOut())
}
