package main

import (
	"bytes"
	"crypto/md5"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
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
	ip             string
	send           chan *message
	sendmarshalled chan *message
	blocksend      chan *message
	banned         chan bool
	stop           chan bool
	user           *User
	ping           chan time.Time
	sync.RWMutex
}

type SimplifiedUser struct {
	Nick     string    `json:"nick,omitempty"`
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
	IsPermanent bool   `json:"ispermanent"`
	Reason      string `json:"reason"`
}

type PingOut struct {
	Timestamp int64 `json:"data"`
}

type message struct {
	msgtyp int
	event  string
	data   interface{}
}

type PrivmsgIn struct {
	Nick string `json:"nick"`
	Data string `json:"data"`
}

type PrivmsgOut struct {
	message
	targetuid Userid
	Messageid int64  `json:"messageid"`
	Timestamp int64  `json:"timestamp"`
	Nick      string `json:"nick,omitempty"`
	Data      string `json:"data,omitempty"`
}

// Create a new connection using the specified socket and router.
func newConnection(s *websocket.Conn, user *User, ip string) {
	c := &Connection{
		socket:         s,
		ip:             ip,
		send:           make(chan *message, SENDCHANNELSIZE),
		sendmarshalled: make(chan *message, SENDCHANNELSIZE),
		blocksend:      make(chan *message),
		banned:         make(chan bool, 8),
		stop:           make(chan bool),
		user:           user,
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

	c.socket.SetReadLimit(MAXMESSAGESIZE)
	c.socket.SetReadDeadline(time.Now().Add(READTIMEOUT))
	c.socket.SetPongHandler(func(string) error {
		c.socket.SetReadDeadline(time.Now().Add(PINGTIMEOUT))
		return nil
	})
	c.socket.SetPingHandler(func(string) error {
		c.sendmarshalled <- &message{
			msgtyp: websocket.PongMessage,
			event:  "PONG",
			data:   []byte{},
		}
		return nil
	})

	if c.user != nil {
		c.rlockUserIfExists()
		n := atomic.LoadInt32(&c.user.connections)
		if n > 5 {
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

	for {
		msgtype, message, err := c.socket.ReadMessage()
		if err != nil || msgtype == websocket.BinaryMessage {
			return
		}

		name, data, err := Unpack(string(message))
		if err != nil {
			// invalid protocol message from the client, just ignore it,
			// disconnect the user
			return
		}

		// dispatch
		switch name {
		case "MSG":
			c.OnMsg(data)
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
		case "BROADCAST":
			c.OnBroadcast(data)
		case "PRIVMSG":
			c.OnPrivmsg(data)
		}
	}
}

func (c *Connection) write(mt int, payload []byte) error {
	c.socket.SetWriteDeadline(time.Now().Add(WRITETIMEOUT))
	return c.socket.WriteMessage(mt, payload)
}

func (c *Connection) writePumpText() {
	defer func() {
		hub.unregister <- c
		c.socket.Close() // Necessary to force reading to stop, will start the cleanup
	}()

	for {
		select {
		case _, ok := <-c.ping:
			if !ok {
				return
			}
			m, _ := time.Now().MarshalBinary()
			if err := c.write(websocket.PingMessage, m); err != nil {
				return
			}
		case <-c.banned:
			c.write(websocket.TextMessage, []byte(`ERR "banned"`))
			c.write(websocket.CloseMessage, []byte{})
			return
		case <-c.stop:
			return
		case m := <-c.blocksend:
			c.rlockUserIfExists()
			if data, err := json.Marshal(m.data); err == nil {
				c.runlockUserIfExists()
				if data := Pack(m.event, data); err == nil {
					if err := c.write(websocket.TextMessage, data); err != nil {
						return
					}
				}
			} else {
				c.runlockUserIfExists()
			}
		case m := <-c.send:
			c.rlockUserIfExists()
			if data, err := json.Marshal(m.data); err == nil {
				c.runlockUserIfExists()
				if data := Pack(m.event, data); err == nil {
					typ := m.msgtyp
					if typ == 0 {
						typ = websocket.TextMessage
					}
					if err := c.write(typ, data); err != nil {
						return
					}
				}
			} else {
				c.runlockUserIfExists()
			}
		case message := <-c.sendmarshalled:
			data := Pack(message.event, message.data.([]byte))
			typ := message.msgtyp
			if typ == 0 {
				typ = websocket.TextMessage
			}
			if err := c.write(typ, data); err != nil {
				return
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
	marshalled, _ := json.Marshal(data)
	c.runlockUserIfExists()

	m := &message{
		event: event,
		data:  marshalled,
	}
	hub.broadcast <- m
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
	out := &EventDataOut{
		Timestamp: unixMilliTime(),
	}
	if c.user != nil {
		out.SimplifiedUser = c.user.simplified
	}
	return out
}

func (c *Connection) Join() {
	if c.user != nil {
		c.rlockUserIfExists()
		defer c.runlockUserIfExists()
		n := atomic.LoadInt32(&c.user.connections)
		if n == 1 {
			c.Broadcast("JOIN", c.getEventDataOut())
		}
	}
}

func (c *Connection) Quit() {
	if c.user != nil {
		c.rlockUserIfExists()
		defer c.runlockUserIfExists()
		n := atomic.LoadInt32(&c.user.connections)
		if n <= 0 {
			c.Broadcast("QUIT", c.getEventDataOut())
		}
	}
}

func (c *Connection) OnBroadcast(data []byte) {
	m := &EventDataIn{}
	if err := json.Unmarshal(data, m); err != nil {
		c.SendError("protocolerror")
		return
	}

	if c.user == nil {
		c.SendError("needlogin")
		return
	}

	if !c.user.featureGet(ISADMIN) {
		c.SendError("nopermission")
		return
	}

	msg := strings.TrimSpace(m.Data)
	msglen := utf8.RuneCountInString(msg)
	if !utf8.ValidString(msg) || msglen == 0 || msglen > 512 || invalidmessage.MatchString(msg) {
		c.SendError("invalidmsg")
		return
	}

	out := c.getEventDataOut()
	out.Data = msg
	c.Broadcast("BROADCAST", out)

}

func (c *Connection) canMsg(msg string, ignoresilence bool) bool {

	msglen := utf8.RuneCountInString(msg)
	if !utf8.ValidString(msg) || msglen == 0 || msglen > 512 || invalidmessage.MatchString(msg) {
		c.SendError("invalidmsg")
		return false
	}

	if !ignoresilence {
		if mutes.isUserMuted(c) {
			c.SendError("muted")
			return false
		}

		if !hub.canUserSpeak(c) {
			c.SendError("submode")
			return false
		}
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
			return false
		}
		c.user.lastmessagetime = now

	}

	return true
}

func (c *Connection) OnMsg(data []byte) {
	m := &EventDataIn{}
	if err := json.Unmarshal(data, m); err != nil {
		c.SendError("protocolerror")
		return
	}

	if c.user == nil {
		c.SendError("needlogin")
		return
	}

	msg := strings.TrimSpace(m.Data)
	if !c.canMsg(msg, false) {
		return
	}

	// strip off /me for anti-spam purposes
	var bmsg []byte
	if len(msg) > 4 && msg[:4] == "/me " {
		bmsg = []byte(strings.TrimSpace(msg[4:]))
	} else {
		bmsg = []byte(msg)
	}

	tsum := md5.Sum(bmsg)
	sum := tsum[:]
	if !c.user.isBot() && bytes.Equal(sum, c.user.lastmessage) {
		c.user.delayscale++
		c.SendError("duplicate")
		return
	}
	c.user.lastmessage = sum

	out := c.getEventDataOut()
	out.Data = msg
	c.Broadcast("MSG", out)
}

func (c *Connection) OnPrivmsg(data []byte) {
	p := &PrivmsgIn{}
	if err := json.Unmarshal(data, p); err != nil {
		c.SendError("protocolerror")
		return
	}

	if c.user == nil {
		c.SendError("needlogin")
		return
	}

	msg := strings.TrimSpace(p.Data)
	if !c.canMsg(msg, true) {
		return
	}

	uid, _ := usertools.getUseridForNick(p.Nick)
	if uid == 0 || uid == c.user.id {
		c.SendError("notfound")
		return
	}

	if err := api.sendPrivmsg(c.user.id, uid, msg); err != nil {
		D("send error from", c.user.nick, err)
		c.SendError(err.Error())
	} else {
		c.EmitBlock("PRIVMSGSENT", "")
	}

}

func (c *Connection) Names() {
	c.sendmarshalled <- &message{
		event: "NAMES",
		data:  namescache.getNames(),
	}
}

func (c *Connection) OnMute(data []byte) {
	mute := &EventDataIn{} // Data is the nick
	if err := json.Unmarshal(data, mute); err != nil {
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
	if err := json.Unmarshal(data, user); err != nil || utf8.RuneCountInString(user.Data) == 0 {
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
	if err := json.Unmarshal(data, ban); err != nil {
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
	if err := json.Unmarshal(data, user); err != nil {
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
	mutes.unmuteUserid(uid)
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
	if err := json.Unmarshal(data, m); err != nil {
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
