/*
Based on https://github.com/trevex/golem
Licensed under the Apache License, Version 2.0
http://www.apache.org/licenses/LICENSE-2.0.html
*/
package main

import (
	"code.google.com/p/go.net/websocket"
	_ "expvar"
	"fmt"
	"github.com/davecheney/profile"
	conf "github.com/msbranco/goconfig"
	"log"
	"net/http"
	"runtime"
	"time"
)

const (
	WRITETIMEOUT         = 10 * time.Second
	READTIMEOUT          = time.Minute
	PINGINTERVAL         = 10 * time.Second
	PINGTIMEOUT          = 30 * time.Second
	MAXMESSAGESIZE       = 6144 // 512 max chars in a message, 8bytes per chars possible, plus factor in some protocol overhead
	SENDCHANNELSIZE      = 16
	BROADCASTCHANNELSIZE = 256
	DEFAULTBANDURATION   = time.Hour
	DEFAULTMUTEDURATION  = 10 * time.Minute
)

var (
	debuggingenabled = false
	DELAY            = 300 * time.Millisecond
	MAXTHROTTLETIME  = 5 * time.Minute
	authtokenurl     string
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
		nc.AddOption("default", "authtokenurl", "http://www.destiny.gg/Auth/Api")

		nc.AddSection("redis")
		nc.AddOption("redis", "address", "localhost:6379")
		nc.AddOption("redis", "database", "0")
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
	authtokenurl, _ = c.GetString("default", "authtokenurl")
	DELAY = time.Duration(delay)
	MAXTHROTTLETIME = time.Duration(maxthrottletime)

	redisaddr, _ := c.GetString("redis", "address")
	redisdb, _ := c.GetInt64("redis", "database")
	redispw, _ := c.GetString("redis", "password")

	dbtype, _ := c.GetString("database", "type")
	dbdsn, _ := c.GetString("database", "dsn")

	if processes <= 0 {
		processes = int64(runtime.NumCPU())
	}
	runtime.GOMAXPROCS(int(processes))
	go (func() {
		t := time.NewTicker(time.Minute)
		for {
			select {
			case <-t.C:
				runtime.GC()
			}
		}
	})()

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

	initWatchdog()
	initNamesCache()
	initHub()
	initDatabase(dbtype, dbdsn)
	initRedis(redisaddr, redisdb, redispw)

	initMutes()
	initBans()
	initUsers(redisdb)
	initEventlog()

	http.Handle("/ws", websocket.Handler(Handler))

	fmt.Printf("Using %v threads, and listening on: %v\n", processes, addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

func unixMilliTime() int64 {
	return time.Now().UTC().Truncate(time.Millisecond).UnixNano() / int64(time.Millisecond)
}

func Handler(socket *websocket.Conn) {
	defer socket.Close()
	r := socket.Request()
	user, banned := getUser(r)

	if banned {
		websocket.Message.Send(socket, `ERR "banned"`)
		return
	}

	newConnection(socket, user)
}
