package main

import (
	"database/sql"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"github.com/vmihailenco/redis"
	"sync"
	"time"
)

type Bans struct {
	users    map[Userid]time.Time
	userlock sync.RWMutex
	ips      map[string]time.Time
	userips  map[Userid][]string
	iplock   sync.RWMutex // protects both ips/userips
}

var (
	bans = Bans{
		make(map[Userid]time.Time),
		sync.RWMutex{},
		make(map[string]time.Time),
		make(map[Userid][]string),
		sync.RWMutex{},
	}
)

func initBans(redisdb int64) {
	go bans.run(redisdb)
}

func (b *Bans) run(redisdb int64) {
	b.loadActive()
	refreshban := b.setupRefresh(redisdb)
	t := time.NewTicker(time.Minute)
	cp := watchdog.register("ban thread", time.Minute)
	defer watchdog.unregister("ban thread")

	for {
		select {
		case <-t.C:
			cp <- true
			b.clean()
		case m := <-refreshban:
			if m.Err != nil {
				D("Error receiving from redis pub/sub channel refreshbans")
				refreshban = b.setupRefresh(redisdb)
			} else {
				D("Refreshing bans")
				b.loadActive()
			}
		}
	}
}

func (b *Bans) setupRefresh(redisdb int64) chan *redis.Message {
refreshagain:
	c, err := rds.PubSubClient()
	if err != nil {
		B("Unable to create redis pubsub client: ", err)
		time.Sleep(500 * time.Millisecond)
		goto refreshagain
	}
	refreshban, err := c.Subscribe(fmt.Sprintf("refreshbans-%d", redisdb))
	if err != nil {
		B("Unable to subscribe to the redis refreshbans channel: ", err)
		time.Sleep(500 * time.Millisecond)
		goto refreshagain
	}

	return refreshban
}

func (b *Bans) clean() {
	b.userlock.Lock()
	defer b.userlock.Unlock()
	b.iplock.Lock()
	defer b.iplock.Unlock()

	for uid, unbantime := range b.users {
		if isExpired(unbantime) {
			delete(b.users, uid)
			b.userips[uid] = nil
		}
	}

	for ip, unbantime := range b.ips {
		if isExpired(unbantime) {
			delete(b.ips, ip)
		}
	}
}

func (b *Bans) banUser(uid Userid, targetuid Userid, ban *BanIn) {
	var expiretime time.Time

	if ban.Ispermanent {
		expiretime = time.Now().UTC().AddDate(10, 0, 0) // 10 years from now should be enough
	} else {
		expiretime = time.Now().UTC().Add(time.Duration(ban.Duration))
	}

	b.userlock.Lock()
	b.users[targetuid] = expiretime
	b.userlock.Unlock()
	b.log(uid, targetuid, ban, "")

	if ban.BanIP {
		ips := getIPCacheForUser(targetuid)
		if len(ips) == 0 {
			D("No ips found in cache for user", targetuid)
			ips = hub.getIPsForUserid(targetuid)
			if len(ips) == 0 {
				D("No ips found for user (offline)", targetuid)
			}
		}

		b.iplock.Lock()
		defer b.iplock.Unlock()
		for _, ip := range ips {
			b.banIP(targetuid, ip, expiretime, true)
			hub.ipbans <- ip
			b.log(uid, targetuid, ban, ip)
			D("IPBanned user", ban.Nick, targetuid, "with ip:", ip)
		}

	}

	hub.bans <- targetuid
	D("Banned user", ban.Nick, targetuid)
}

func (b *Bans) banIP(uid Userid, ip string, t time.Time, skiplock bool) {
	if !skiplock { // because the caller holds the locks
		b.iplock.Lock()
		defer b.iplock.Unlock()
	}

	b.ips[ip] = t
	if _, ok := b.userips[uid]; !ok {
		b.userips[uid] = make([]string, 0, 1)
	}
	b.userips[uid] = append(b.userips[uid], ip)
}

func (b *Bans) unbanUserid(uid Userid) {
	b.logUnban(uid)
	b.userlock.Lock()
	defer b.userlock.Unlock()
	b.iplock.Lock()
	defer b.iplock.Unlock()

	delete(b.users, uid)
	for _, ip := range b.userips[uid] {
		delete(b.ips, ip)
		D("Unbanned IP: ", ip, "for uid:", uid)
	}
	b.userips[uid] = nil
	D("Unbanned uid: ", uid)
}

func isExpired(t time.Time) bool {
	return t.Before(time.Now().UTC())
}

func isStillBanned(t time.Time, ok bool) bool {
	if !ok {
		return false
	}
	return !isExpired(t)
}

func (b *Bans) isUseridBanned(uid Userid) bool {
	if uid == 0 {
		return false
	}
	b.userlock.RLock()
	defer b.userlock.RUnlock()
	t, ok := b.users[uid]
	return isStillBanned(t, ok)
}

func (b *Bans) isIPBanned(ip string) bool {
	b.iplock.RLock()
	defer b.iplock.RUnlock()
	t, ok := b.ips[ip]
	return isStillBanned(t, ok)
}

func (b *Bans) loadActive() {
	b.userlock.Lock()
	defer b.userlock.Unlock()
	b.iplock.Lock()
	defer b.iplock.Unlock()

	// purge all the bans
	b.users = make(map[Userid]time.Time)
	b.ips = make(map[string]time.Time)
	b.userips = make(map[Userid][]string)

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
		return
	}

	for rows.Next() {
		var uid Userid
		var ipaddress sql.NullString
		var endtimestamp mysql.NullTime
		err = rows.Scan(&uid, &ipaddress, &endtimestamp)

		if err != nil {
			B("Unable to scan row: ", err)
			continue
		}

		if !endtimestamp.Valid {
			endtimestamp.Time = time.Now().UTC().AddDate(10, 0, 0)
		}

		if ipaddress.Valid {
			b.ips[ipaddress.String] = endtimestamp.Time
			if _, ok := b.userips[uid]; !ok {
				b.userips[uid] = make([]string, 0, 1)
			}
			b.userips[uid] = append(b.userips[uid], ipaddress.String)
			hub.ipbans <- ipaddress.String
		} else {
			b.users[uid] = endtimestamp.Time
		}

	}
}

func (b *Bans) log(uid Userid, targetuid Userid, ban *BanIn, ip string) {

	ipaddress := &sql.NullString{}
	if ban.BanIP && len(ip) != 0 {
		ipaddress.String = ip
		ipaddress.Valid = true
	}
	starttimestamp := time.Now().UTC()

	endtimestamp := &mysql.NullTime{}
	if !ban.Ispermanent {
		endtimestamp.Time = starttimestamp.Add(time.Duration(ban.Duration))
		endtimestamp.Valid = true
	}

	_, err := banstatement.Exec(uid, targetuid, ipaddress, ban.Reason, starttimestamp, endtimestamp)

	if err != nil {
		D("Unable to insert ban: ", err)
	}
}

func (b *Bans) logUnban(targetuid Userid) {
	_, err := unbanstatement.Exec(targetuid)

	if err != nil {
		D("Unable to insert ban: ", err)
	}
}
