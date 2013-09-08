package main

import (
	"bytes"
	"net"
)

type IRC struct {
	connections map[*IRCConnection]bool
	broadcast   chan *IRCBroadcast
	register    chan *IRCConnection
	unregister  chan *IRCConnection
	bans        chan Userid
	ipbans      chan string
}

type IRCBroadcast struct {
	message []byte
	except  *IRCConnection
}

func (s *IRC) sendRaw(c *IRCConnection, ss ...string) {

	var b bytes.Buffer
	for _, v := range ss {
		b.Write([]byte(v))
	}
	b.Write([]byte("\r\n"))
	s.broadcast <- &IRCBroadcast{b.Bytes(), c}

}

func initIRC(addr string) {
	go irchub.run()
	irchub.accept(addr)
}

func (s *IRC) accept(addr string) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		F("unable to listen on irc addr", err)
	}
	defer l.Close()
	for {
		c, err := l.Accept()
		if err != nil {
			F("unable to accept", err)
		}

		go handleIRC(c)
	}
}

func (s *IRC) run() {
	for {
		select {
		case c := <-s.register:
			s.connections[c] = true
		case c := <-s.unregister:
			delete(s.connections, c)
		case b := <-s.broadcast:
			for c, _ := range s.connections {
				if c != b.except && len(c.sendmarshalled) < SENDCHANNELSIZE {
					c.sendmarshalled <- b.message
				}
			}
		}
	}
}
