package main

import (
	"bufio"
	"net"
	"regexp"
	"strings"
	"time"
)

type IRCMessage struct {
	Prefix     string
	Nick       string
	Ident      string
	Host       string
	Command    string
	Parameters []string
	Message    string
}

var (
	// 1 prefix
	// 2 command
	// 3 arguments
	ircre       = regexp.MustCompile(`^(?:[:@]([^ ]+) +)?(?:([^\r\n ]+) *)([^\r\n]*)[\r\n]{1,2}$`)
	ircargre    = regexp.MustCompile(`(.*?) :(.*)$`)
	ircprefixre = regexp.MustCompile(`^([^!]*)(?:!([^@]+)@(.*))?$`)
)
var irchub = IRC{
	connections: make(map[*IRCConnection]bool),
	broadcast:   make(chan *IRCBroadcast, SENDCHANNELSIZE),
	register:    make(chan *IRCConnection),
	unregister:  make(chan *IRCConnection, SENDCHANNELSIZE),
	bans:        make(chan Userid),
	ipbans:      make(chan string),
}

func parseIRC(b string) *IRCMessage {
	matches := ircre.FindStringSubmatch(b)
	if matches == nil {
		return nil
	}

	m := &IRCMessage{
		Prefix:  matches[1],
		Command: matches[2],
	}

	args := ircargre.FindStringSubmatch(matches[3])
	if args != nil {
		m.Parameters = strings.SplitN(args[1], " ", 15)
		m.Message = args[2]
	} else if matches[3] != "" && matches[3][0:1] == ":" {
		m.Message = matches[3][1:]
	} else {
		m.Parameters = strings.SplitN(matches[3], " ", 15)
	}

	usermatches := ircprefixre.FindStringSubmatch(matches[1])
	if usermatches != nil {
		m.Nick = usermatches[1]
		m.Ident = usermatches[2]
		m.Host = usermatches[3]
	}

	return m
}

func initIRC(addr string) {
	go irchub.accept(addr)
	go irchub.run()
}

/*
<< CAP LS

[08.31|20:21:24] << PASS password
[08.31|20:21:24] << NICK sztanpet
[08.31|20:21:24] >> :irc.example.net 001 sztanpet :Welcome to the Internet Relay Network sztanpet!~sztanpet@melkor
[08.31|20:22:43] >> :irc.example.net 002 sztanpet :Your host is irc.example.net, running version ngircd-18 (x86_64/pc/linux-gnu)
[08.31|20:22:43] >> :irc.example.net 003 sztanpet :This server has been started Thu Aug 29 2013 at 08:27:34 (CEST)
[08.31|20:22:43] >> :irc.example.net 004 sztanpet irc.example.net ngircd-18 aciorswx bmov
[08.31|20:22:43] >> :irc.example.net 005 sztanpet RFC2812 IRCD=shitstiny CASEMAPPING=ascii PREFIX=(qohv)&@%+ CHANTYPES=# CHANMODES=b,,,m CHANLIMIT=#:1 :are supported on this server
[08.31|20:22:43] >> :irc.example.net 005 sztanpet CHANNELLEN=10 NICKLEN=20 FNC KICKLEN=400 PENALTY :are supported on this server
[08.31|20:22:43] << USERHOST sztanpet

[08.31|20:22:44] >> :irc.example.net 302 sztanpet :sztanpet=+~sztanpet@melkor
USER
<< JOIN #obsproject

[09.01|15:11:47] >> :sztanpet!~sztanpet@melkor JOIN :#obsproject
[09.01|15:11:47] << MODE #obsproject

[09.01|15:11:47] << WHO #obsproject

[09.01|15:11:47] >> :irc.example.net 353 sztanpet = #obsproject :@sztanpet
[09.01|15:11:47] >> :irc.example.net 366 sztanpet #obsproject :End of NAMES list
[09.01|15:11:47] >> :irc.example.net 324 sztanpet #obsproject +
[09.01|15:11:47] >> :irc.example.net 329 sztanpet #obsproject 1378041104
[09.01|15:11:47] >> :irc.example.net 352 sztanpet #obsproject ~sztanpet melkor irc.example.net sztanpet H@ :0 sztanpet
[09.01|15:11:47] >> :irc.example.net 315 sztanpet #obsproject :End of WHO list
*/
