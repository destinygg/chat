package main

import (
	"database/sql"
	"github.com/go-sql-driver/mysql"
	"github.com/vmihailenco/redis"
	"time"
)

type Bans struct {
	users          map[Userid]time.Time
	ips            map[string]time.Time
	userips        map[Userid][]string
	isipbanned     chan *isIPBanned
	banip          chan *banIP
	isuseridbanned chan *isUseridBanned
	banuserid      chan *banUserid
	unbanuserid    chan Userid
}

type isIPBanned struct {
	ip     string
	banned chan bool
}

type banIP struct {
	userid Userid
	ip     string
	t      time.Time
}

type isUseridBanned struct {
	userid Userid
	banned chan bool
}

type banUserid struct {
	userid Userid
	t      time.Time
}

var (
	bans = Bans{
		make(map[Userid]time.Time),
		make(map[string]time.Time),
		make(map[Userid][]string),
		make(chan *isIPBanned),
		make(chan *banIP),
		make(chan *isUseridBanned),
		make(chan *banUserid),
		make(chan Userid),
	}
)

func initBans() {
	go bans.run()
}

func (b *Bans) run() {
	b.loadActive()
	refreshban := b.setupRefresh()
	t := time.NewTicker(time.Minute)
	cp := registerWatchdog("ban thread", time.Minute)
	defer unregisterWatchdog("ban thread")

	for {
		select {
		case <-t.C:
			cp <- true
			b.clean()
		case uid := <-b.unbanuserid:
			delete(b.users, uid)
			for _, ip := range b.userips[uid] {
				delete(b.ips, ip)
				D("Unbanned IP: ", ip, "for userid:", uid)
			}
			b.userips[uid] = nil
		case d := <-b.banuserid:
			b.users[d.userid] = d.t
		case d := <-b.isuseridbanned:
			if t, ok := b.users[d.userid]; ok {
				d.banned <- t.After(time.Now().UTC())
			} else {
				d.banned <- false
			}
		case d := <-b.banip:
			b.ips[d.ip] = d.t
			if _, ok := b.userips[d.userid]; !ok {
				b.userips[d.userid] = make([]string, 0, 1)
			}
			b.userips[d.userid] = append(b.userips[d.userid], d.ip)
		case d := <-b.isipbanned:
			if t, ok := b.ips[d.ip]; ok {
				d.banned <- t.After(time.Now().UTC())
			} else {
				d.banned <- false
			}
		case m := <-refreshban:
			if m.Err != nil {
				D("Error receiving from redis pub/sub channel refreshbans")
				refreshban = b.setupRefresh()
			} else {
				D("Refreshing bans")
				b.loadActive()
			}
		}
	}
}

func (b *Bans) setupRefresh() chan *redis.Message {
	c, err := rds.PubSubClient()
	if err != nil {
		B("Unable to create redis pubsub client: ", err)
		time.Sleep(500 * time.Millisecond)
		return b.setupRefresh()
	}
	refreshban, err := c.Subscribe("refreshbans")
	if err != nil {
		B("Unable to subscribe to the redis refreshbans channel: ", err)
		time.Sleep(500 * time.Millisecond)
		return b.setupRefresh()
	}

	return refreshban
}

func (b *Bans) clean() {
	delcount := 0
	for userid, unbantime := range b.users {
		if unbantime.Before(time.Now().UTC()) {
			delete(b.users, userid)
			b.userips[userid] = nil
			delcount++
		}
	}

	delcount = 0
	for ip, unbantime := range b.ips {
		if unbantime.Before(time.Now().UTC()) {
			delete(b.ips, ip)
			delcount++
		}
	}
}

func (b *Bans) banUser(userid Userid, targetuserid Userid, ban *BanIn) {
	var expiretime time.Time

	if ban.Ispermanent {
		expiretime = time.Now().UTC().AddDate(10, 0, 0) // 10 years from now should be enough
	} else {
		expiretime = time.Now().UTC().Add(time.Duration(ban.Duration))
	}

	b.banuserid <- &banUserid{targetuserid, expiretime}
	b.log(userid, targetuserid, ban, "")

	if ban.BanIP {
		ips := getIPCacheForUser(targetuserid)
		if len(ips) == 0 {
			D("No ips found in cache for user", targetuserid)
			ips = hub.getIPsForUserid(targetuserid)
			if len(ips) == 0 {
				D("No ips found for user (offline)", targetuserid)
			}
		}
		for _, ip := range ips {
			b.banip <- &banIP{targetuserid, ip, expiretime}
			hub.ipbans <- ip
			b.log(userid, targetuserid, ban, ip)
			D("IPBanned user", ban.Nick, targetuserid, "with ip:", ip)
		}
	}

	hub.bans <- targetuserid
	D("Banned user", ban.Nick, targetuserid)
}

func (b *Bans) unbanUserid(userid Userid) {
	b.logUnban(userid)
	b.unbanuserid <- userid
	D("Unbanned userid: ", userid)
}

func (b *Bans) isUseridIPBanned(ip string, uid Userid) bool {
	c := make(chan bool, 1)
	b.isipbanned <- &isIPBanned{ip, c}
	res := <-c
	if res || uid == 0 {
		return res
	}

	b.isuseridbanned <- &isUseridBanned{uid, c}
	res = <-c
	return res
}

func (b *Bans) loadActive() {
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

func (b *Bans) log(userid Userid, targetuserid Userid, ban *BanIn, ip string) {

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

	_, err := banstatement.Exec(userid, targetuserid, ipaddress, ban.Reason, starttimestamp, endtimestamp)

	if err != nil {
		D("Unable to insert ban: ", err)
	}
}

func (b *Bans) logUnban(targetuserid Userid) {
	_, err := unbanstatement.Exec(targetuserid)

	if err != nil {
		D("Unable to insert ban: ", err)
	}
}
