package main

import (
	"database/sql"
	"github.com/go-sql-driver/mysql"
	"net"
	"sync"
	"time"
)

type Mutes struct {
	users map[Userid]time.Time
	sync.RWMutex
}

var mutes *Mutes

func initMutes() {
	mutes = &Mutes{
		make(map[Userid]time.Time),
		sync.RWMutex{},
	}
	time.AfterFunc(CLEANMUTESBANSPERIOD, cleanMutes)
}

func cleanMutes() {
	mutes.Lock()
	defer mutes.Unlock()
	delcount := 0
	for userid, unmutetime := range mutes.users {
		if unmutetime.Before(time.Now()) {
			delete(mutes.users, userid)
			delcount++
		}
	}
	D("Cleaned mutes, deleted records: ", delcount)
	time.AfterFunc(CLEANMUTESBANSPERIOD, cleanMutes)
}

func muteUserid(userid Userid, duration int64) {
	mutes.Lock()
	mutes.users[userid] = time.Now().UTC().Add(time.Duration(duration))
	mutes.Unlock()
	hub.mutes <- userid
}

func unmuteUserid(userid Userid) {
	mutes.Lock()
	delete(mutes.users, userid)
	mutes.Unlock()
	D("Unmuted userid: ", userid)
}

func isUserMuted(conn *Connection) bool {
	if conn.user == nil {
		return true
	}
	userid := conn.user.id
	mutes.RLock()
	unmutetime, ok := mutes.users[userid]
	mutes.RUnlock()
	if !ok || time.Now().UTC().After(unmutetime) {
		return false
	}
	return true
}

// ---------------

type Bans struct {
	users map[Userid]time.Time
	ips   map[string]time.Time
	sync.RWMutex
}

var bans *Bans

func initBans() {
	bans = &Bans{
		make(map[Userid]time.Time),
		make(map[string]time.Time),
		sync.RWMutex{},
	}
	time.AfterFunc(CLEANMUTESBANSPERIOD, cleanBans)
	loadActiveBans()

	c, err := rds.PubSubClient()
	if err != nil {
		B("Unable to create redis pubsub client: ", err)
	}
	refreshban, err := c.Subscribe("refreshbans")
	if err != nil {
		B("Unable to subscribe to the redis refreshbans channel: ", err)
	}
	go (func() {
		for {
			select {
			case <-refreshban:
				loadActiveBans()
			}
		}
	})()

}

func cleanBans() {
	bans.Lock()
	defer bans.Unlock()
	delcount := 0
	for userid, unbantime := range bans.users {
		if unbantime.Before(time.Now()) {
			delete(bans.users, userid)
			delcount++
		}
	}
	DP("Cleaning bans, expired bans: ", delcount)
	delcount = 0
	for ip, unbantime := range bans.ips {
		if unbantime.Before(time.Now().UTC()) {
			delete(bans.ips, ip)
			delcount++
		}
	}
	D("expired ipbans: ", delcount)
	time.AfterFunc(CLEANMUTESBANSPERIOD, cleanBans)
}

func banUser(userid Userid, ban *BanIn) {
	bans.Lock()
	bans.users[userid] = time.Now().UTC().Add(time.Duration(ban.Duration))
	bans.Unlock()
	hub.bans <- userid
	D("Banned userid: ", userid)
}

func unbanUserid(userid Userid) {
	bans.Lock()
	delete(bans.users, userid)
	bans.Unlock()
	D("Unbanned userid: ", userid)
}

func ipBanUser(conn *Connection) {
	ip := conn.socket.RemoteAddr().(*net.TCPAddr).IP
	bans.Lock()
	bans.ips[ip.String()] = time.Now().UTC().Add(7 * 24 * time.Hour)
	bans.Unlock()
	hub.ipbans <- ip
	D("IPBanned user with ip: ", ip.String())
}

func isUserBanned(conn *Connection) bool {
	bans.RLock()
	defer bans.RUnlock()

	ip := conn.socket.RemoteAddr().(*net.TCPAddr).IP.String()
	unbantime, ok := bans.ips[ip]
	if ok && unbantime.After(time.Now().UTC()) {
		return true
	}

	if conn.user != nil {
		unbantime, ok := bans.users[conn.user.id]
		if ok && unbantime.After(time.Now().UTC()) {
			return true
		}
	}

	return false
}

func isUseridIPBanned(ip string, uid Userid) bool {
	bans.RLock()
	defer bans.RUnlock()

	unbantime, ok := bans.ips[ip]
	if ok && unbantime.After(time.Now().UTC()) {
		return true
	}

	if uid != 0 {
		unbantime, ok := bans.users[uid]
		if ok && unbantime.After(time.Now().UTC()) {
			return true
		}
	}

	return false
}

func loadActiveBans() {
	bans.Lock()
	defer bans.Unlock()

	// purge all the bans
	bans.users = make(map[Userid]time.Time)
	bans.ips = make(map[string]time.Time)

	rows, err := db.Query(`
		SELECT
			targetuserid,
			ipaddress,
			endtimestamp
		FROM bans
		WHERE
			endtimestamp IS NULL OR
			endtimestamp > NOW()
		GROUP BY targetuserid, ipaddress
	`)

	if err != nil {
		B("Unable to get active bans: ", err)
	}

	for rows.Next() {
		var uid Userid
		var ipaddress sql.NullString
		var endtimestamp mysql.NullTime
		err = rows.Scan(&uid, &ipaddress, &endtimestamp)

		if err != nil {
			B("Unable to scan row: ", err)
		}

		if !endtimestamp.Valid {
			endtimestamp.Time = time.Now().UTC().AddDate(10, 0, 0)
		}

		if ipaddress.Valid {
			bans.ips[ipaddress.String] = endtimestamp.Time
		} else {
			bans.users[uid] = endtimestamp.Time
		}

	}
}
