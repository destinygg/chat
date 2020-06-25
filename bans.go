package main

import (
	"database/sql"
	"net"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/tideland/golib/redis"
)

type Bans struct {
	users    map[Userid]time.Time
	userlock sync.RWMutex
	ips      map[string]time.Time
	userips  map[Userid][]string
	iplock   sync.RWMutex // protects both ips/userips
}

var (
	ipv6mask = net.CIDRMask(64, 128)
	bans     = Bans{
		make(map[Userid]time.Time),
		sync.RWMutex{},
		make(map[string]time.Time),
		make(map[Userid][]string),
		sync.RWMutex{},
	}
)

func getMaskedIP(s string) string {
	ip := net.ParseIP(s)
	if ip.To4() == nil {
		return ip.Mask(ipv6mask).String()
	} else {
		return s
	}
}

func initBans(redisdb int64) {
	go bans.run(redisdb)
}

func (b *Bans) run(redisdb int64) {
	b.loadActive()

	go b.runRefresh(redisdb)
	go b.runUnban(redisdb)

	t := time.NewTicker(time.Minute)

	for {
		select {
		case <-t.C:
			b.clean()
		}
	}
}

func (b *Bans) runRefresh(redisdb int64) {
	setupRedisSubscription("refreshbans", redisdb, func(result *redis.PublishedValue) {
		D("Refreshing bans")
		b.loadActive()
	})
}

func (b *Bans) runUnban(redisdb int64) {
	setupRedisSubscription("unbanuserid", redisdb, func(result *redis.PublishedValue) {
		userid, err := result.Value.Uint64()
		if err != nil {
			D("Error parsing message as uint64:", userid, err)
			return
		}

		uid := Userid(userid)
		b.unbanUserid(uid)
		mutes.unmuteUserid(uid)
	})
}

func (b *Bans) clean() {
	b.userlock.Lock()
	defer b.userlock.Unlock()
	b.iplock.Lock()
	defer b.iplock.Unlock()

	for uid, unbantime := range b.users {
		if isExpiredUTC(unbantime) {
			delete(b.users, uid)
			b.userips[uid] = nil
		}
	}

	for ip, unbantime := range b.ips {
		if isExpiredUTC(unbantime) {
			delete(b.ips, ip)
		}
	}
}

func (b *Bans) banUser(uid Userid, targetuid Userid, ban *BanIn) {
	var expiretime time.Time

	if ban.Ispermanent {
		expiretime = getFuturetimeUTC()
	} else {
		expiretime = addDurationUTC(time.Duration(ban.Duration))
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

func isStillBanned(t time.Time, ok bool) bool {
	if !ok {
		return false
	}
	return !isExpiredUTC(t)
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

	db.getBans(func(uid Userid, ipaddress sql.NullString, endtimestamp mysql.NullTime) {
		if !endtimestamp.Valid {
			endtimestamp.Time = getFuturetimeUTC()
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
	})
}

func (b *Bans) log(uid Userid, targetuid Userid, ban *BanIn, ip string) {
	db.insertBan(uid, targetuid, ban, ip)
}

func (b *Bans) logUnban(targetuid Userid) {
	db.deleteBan(targetuid)
}
