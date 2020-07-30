/*
Based on https://github.com/trevex/golem
Licensed under the Apache License, Version 2.0
http://www.apache.org/licenses/LICENSE-2.0.html
*/
package main

import (
	"bytes"
	"encoding/gob"
	_ "expvar"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	//_ "github.com/mkevac/debugcharts"
	conf "github.com/msbranco/goconfig"
)

type State struct {
	mutes   map[Userid]time.Time
	submode bool
	sync.RWMutex
}

var (
	state = &State{
		mutes: make(map[Userid]time.Time),
	}
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

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
		nc.AddOption("redis", "database", "0")
		nc.AddOption("redis", "password", "")

		nc.AddSection("database")
		nc.AddOption("database", "type", "mysql")
		nc.AddOption("database", "dsn", "username:password@tcp(localhost:3306)/destinygg?loc=UTC&parseTime=true&strict=true&timeout=1s&time_zone=\"+00:00\"")

		nc.AddSection("api")
		nc.AddOption("api", "url", "http://www.destiny.gg/api")
		nc.AddOption("api", "key", "changeme")

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
	apiurl, _ := c.GetString("api", "url")
	apikey, _ := c.GetString("api", "key")
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

	state.load()

	initApi(apiurl, apikey)
	initRedis(redisaddr, redisdb, redispw)

	initNamesCache()
	initHub()
	initDatabase(dbtype, dbdsn)

	initBroadcast(redisdb)
	initBans(redisdb)
	initUsers(redisdb)

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}

		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		user, banned, ip := getUserFromWebRequest(r)

		if banned {
			ws.SetWriteDeadline(time.Now().Add(WRITETIMEOUT))
			ws.WriteMessage(websocket.TextMessage, []byte(`ERR "banned"`))
			return
		}

		newConnection(ws, user, ip)
	})

	fmt.Printf("Using %v threads, and listening on: %v\n", processes, addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

func unixMilliTime() int64 {
	return time.Now().UTC().Truncate(time.Millisecond).UnixNano() / int64(time.Millisecond)
}

// expecting the argument to be in UTC
func isExpiredUTC(t time.Time) bool {
	return t.Before(time.Now().UTC())
}

func addDurationUTC(d time.Duration) time.Time {
	return time.Now().UTC().Add(d)
}

func getFuturetimeUTC() time.Time {
	return time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC)
}

func (s *State) load() {
	s.Lock()
	defer s.Unlock()

	b, err := ioutil.ReadFile(".state.dc")
	if err != nil {
		D("Error while reading from states file", err)
		return
	}
	mb := bytes.NewBuffer(b)
	dec := gob.NewDecoder(mb)
	err = dec.Decode(&s.mutes)
	if err != nil {
		D("Error decoding mutes from states file", err)
	}
	err = dec.Decode(&s.submode)
	if err != nil {
		D("Error decoding submode from states file", err)
	}
}

// expects to be called with locks held
func (s *State) save() {
	mb := new(bytes.Buffer)
	enc := gob.NewEncoder(mb)
	err := enc.Encode(&s.mutes)
	if err != nil {
		D("Error encoding mutes:", err)
	}
	err = enc.Encode(&s.submode)
	if err != nil {
		D("Error encoding submode:", err)
	}

	err = ioutil.WriteFile(".state.dc", mb.Bytes(), 0600)
	if err != nil {
		D("Error with writing out state file:", err)
	}
}
