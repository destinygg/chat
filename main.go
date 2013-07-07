/*
Based on https://github.com/trevex/golem
Licensed under the Apache License, Version 2.0
http://www.apache.org/licenses/LICENSE-2.0.html
*/
package main

import (
	_ "expvar"
	"fmt"
	"github.com/davecheney/profile"
	"github.com/garyburd/go-websocket/websocket"
	conf "github.com/msbranco/goconfig"
	"log"
	"net/http"
	"runtime"
	"time"
)

const (
	WRITETIMEOUT         = 10 * time.Second
	READTIMEOUT          = 1 * time.Minute
	PINGINTERVAL         = 10 * time.Second
	PINGTIMEOUT          = 30 * time.Second
	MAXMESSAGESIZE       = 6144 // 512 max chars in a message, 8bytes per chars possible, plus factor in some protocol overhead
	SENDCHANNELSIZE      = 16
	BROADCASTCHANNELSIZE = 256
	CLEANMUTESBANSPERIOD = time.Hour
	DEFAULTBANDURATION   = 10 * time.Minute
	DEFAULTMUTEDURATION  = time.Minute
	SCROLLBACKLINES      = "200"
)

const (
	ISADMIN      = 1 << iota
	ISMODERATOR  = 1 << iota
	ISVIP        = 1 << iota
	ISPROTECTED  = 1 << iota
	ISSUBSCRIBER = 1 << iota
	ISBOT        = 1 << iota
)

type message struct {
	event string
	data  interface{}
}

var (
	debuggingenabled = false
	DELAY            = 300 * time.Millisecond
	MAXTHROTTLETIME  = 5 * time.Minute
)

func main() {

	c, err := conf.ReadConfigFile("settings.cfg")
	if err != nil {
		nc := conf.NewConfigFile()
		nc.AddOption("default", "debug", "false")
		nc.AddOption("default", "listenaddress", ":9998")
		nc.AddOption("default", "maxprocesses", "0")
		nc.AddOption("default", "chatdelay", fmt.Sprintf("%d", 300*time.Millisecond))
		nc.AddOption("default", "maxthrottletime", fmt.Sprintf("%d", 5*time.Minute))

		nc.AddSection("redis")
		nc.AddOption("redis", "address", "localhost:6379")
		nc.AddOption("redis", "database", "-1")
		nc.AddOption("redis", "password", "")

		nc.AddSection("database")
		nc.AddOption("database", "type", "mysql")
		nc.AddOption("database", "dsn", "username:password@tcp(localhost:3306)/destinygg?loc=UTC&parseTime=true&strict=true&timeout=1s&time_zone=\"+00:00\"")

		if err := nc.WriteConfigFile("settings.cfg", 0644, "DestinyChatBackend"); err != nil {
			log.Fatal("Unable to create settings.cfg: ", err)
		}
		if c, err = conf.ReadConfigFile("settings.cfg"); err != nil {
			log.Fatal("Unable to read settings.cfg: ", err)
		}
	}

	debuggingenabled, _ = c.GetBool("default", "debug")
	addr, _ := c.GetString("default", "listenaddress")
	processes, _ := c.GetInt64("default", "maxprocesses")
	delay, _ := c.GetInt64("default", "chatdelay")
	maxthrottletime, _ := c.GetInt64("default", "maxthrottletime")
	DELAY = time.Duration(delay)
	MAXTHROTTLETIME = time.Duration(maxthrottletime)

	redisaddr, _ := c.GetString("redis", "address")
	redisdb, _ := c.GetInt64("redis", "database")
	redispw, _ := c.GetString("redis", "password")

	dbtype, _ := c.GetString("database", "type")
	dbdsn, _ := c.GetString("database", "dsn")

	if processes <= 0 {
		processes = int64(runtime.NumCPU()) * 2
	}
	runtime.GOMAXPROCS(int(processes))

	if debuggingenabled {
		defer profile.Start(&profile.Config{
			Quiet:          false,
			CPUProfile:     true,
			MemProfile:     true,
			BlockProfile:   true,
			ProfilePath:    "./",
			NoShutdownHook: false,
		}).Stop()
	}

	go hub.run()
	initDatabase(dbtype, dbdsn)
	initRedis(redisaddr, redisdb, redispw)

	initMutes()
	initBans()
	initUsers()
	initEventlog()

	http.HandleFunc("/ws", Handler)

	fmt.Printf("Using %v threads, and listening on: %v\n", processes, addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

func unixMilliTime() int64 {
	return time.Now().UTC().Truncate(time.Millisecond).UnixNano() / int64(time.Millisecond)
}

func Handler(w http.ResponseWriter, r *http.Request) {
	// Check if method used was GET.
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", 405)
		return
	}

	user, banned := getUser(r)
	if banned {
		http.Error(w, "Authorization failed", 403)
		return
	}

	socket, err := websocket.Upgrade(w, r.Header, nil, 1024, 1024)
	if _, ok := err.(websocket.HandshakeError); ok {
		http.Error(w, "Not a websocket handshake", 400)
		return
	}

	newConnection(socket, user)

}
