package main

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"sync"
	"time"
)

type IRCConnection struct {
	socket         net.Conn
	connected      bool
	read           chan *IRCMessage
	send           chan *message
	sendmarshalled chan []byte
	blocksend      chan *blocksend
	banned         chan bool
	stop           chan bool
	user           *User
	prefix         string
	lastactive     time.Time
	ping           chan time.Time
	sync.RWMutex
}

type blocksend struct {
	message []byte
	C       chan bool
}

func handleIRC(s net.Conn) {
	defer s.Close()

	c := &IRCConnection{
		socket:         s,
		read:           make(chan *IRCMessage),
		sendmarshalled: make(chan []byte, SENDCHANNELSIZE),
		blocksend:      make(chan *blocksend),
		banned:         make(chan bool, 8),
		stop:           make(chan bool),
		lastactive:     time.Time{},
		ping:           make(chan time.Time, 2),
		RWMutex:        sync.RWMutex{},
	}

	go c.writePump()
	go c.readPump()

	deadline := time.NewTimer(500 * time.Millisecond)

	for {
		select {
		case m, ok := <-c.read:
			D(m, ok)
			if m == nil {
				continue
			}

			switch m.Command {
			case "PASS":
				c.authUser(m)
				if c.connected {
					deadline.Stop()
					c.sendInfo()
					irchub.register <- c
					println("registered")
				}
			}
		case <-deadline.C:
			ch := make(chan bool, 1)
			c.blocksend <- &blocksend{
				[]byte("NOTICE AUTH :Wrong or no password provided (your password should be your login key from the Profile/Authentication section on destiny.gg)\r\n"),
				ch,
			}
			<-ch
			return
		}

	}

}

func (c *IRCConnection) readPump() {
	defer c.socket.Close()

	buff := bufio.NewReader(c.socket)
	for {
		line, err := buff.ReadString('\n')
		if err != nil {
			return
		}
		DP("< ", line)
		c.read <- parseIRC(line)
	}
}

func (c *IRCConnection) write(b []byte) {
	DP("> ", string(b))
	written := 0
write:
	c.socket.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
	n, err := c.socket.Write(b[written:])
	if err != nil {
		P("Write error: ", err)
		return
	}
	written += n
	if written < len(b) {
		goto write
	}
}

func (c *IRCConnection) writePump() {
	defer c.socket.Close()

	for {
		select {
		case bs := <-c.blocksend:
			c.write(bs.message)
			bs.C <- true
		case b, ok := <-c.sendmarshalled:
			if !ok {
				return
			}
			c.write(b)
		}
	}
}

func (c *IRCConnection) sendRaw(s ...string) {
	var b bytes.Buffer
	for _, v := range s {
		b.Write([]byte(v))
	}
	b.Write([]byte("\r\n"))
	c.sendmarshalled <- b.Bytes()
}

// send our 005, the nick of the user, and make him join the channel
func (c *IRCConnection) sendInfo() {
	c.sendRaw(":", c.prefix, " NICK :", c.user.nick)
	c.sendRaw(":www.destiny.gg 001 ", nick, " :Welcome to the Internet Relay Network ", nick, "!", nick, "@", "cloaked.destiny.gg")
	c.sendRaw(":www.destiny.gg 002 ", nick, " :Your host is www.destiny.gg, running shitstiny (https://github.com/destinygg/chat)")
	c.sendRaw(":www.destiny.gg 003 ", nick, " :This server has been started Sun Sep 07 2013 at 18:45:00 (CEST)")
	c.sendRaw(":www.destiny.gg 004 ", nick, " www.destiny.gg shitstiny aciorswx bmov")
	c.sendRaw(":www.destiny.gg 005 ", nick, " RFC2812 IRCD=shitstiny CASEMAPPING=ascii PREFIX=(qohv)&@%+ CHANTYPES=# CHANMODES=b,,,m CHANLIMIT=#:1 :are supported on this server")
	c.sendRaw(":www.destiny.gg 005 ", nick, " CHANNELLEN=10 NICKLEN=20 FNC KICKLEN=400 PENALTY :are supported on this server")
	c.sendRaw(":", c.prefix, " JOIN :#destiny")
	c.sendNames()
}

func (c *IRCConnection) sendNames() {
	namelines := namescache.getIrcNames()
	l := 0
	var namelines [][]string
	var names []string
	for i, name := range allnames {
		if l+len(name) > 400 {
			namelines = append(namelines, names)
			l = 0
			names = nil
			names = append(names, name)
			l += len(name)
		} else {
			names = append(names, name)
			l += len(name)
		}
	}
	for _, names := range namelines {
		c.sendRaw(":www.destiny.gg 353 ", c.user.nick, " = #destiny :", names...)
	}
	c.sendRaw(":www.destiny.gg 366 ", c.user.nick, " #destiny :End of /NAMES list.")
}

func ircMarshal(event string, data *EventDataOut) {
	switch event {
	case "MSG":

	}
}

func (c *IRCConnection) authUser(m *IRCMessage) {
	if c.connected {
		return
	}

	if m.Command != "PASS" ||
		len(m.Parameters) != 1 ||
		!cookievalid.MatchString(m.Parameters[0]) {
		continue
	}

	resp, err := http.PostForm(authtokenurl, url.Values{"authtoken": {m.Parameters[0]}})
	if resp.Body != nil {
		defer resp.Body.Close()
	}

	if err != nil || resp.StatusCode != 200 {
		return
	}

	authdata, _ = ioutil.ReadAll(resp.Body)

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
	c.user = namescache.add(user)
	c.prefix = fmt.Sprintf("%s!~%s@cloaked.destiny.gg", user.nick, user.nick)
	c.connected = true
}
