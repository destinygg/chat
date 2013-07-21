package main

import (
	"database/sql"
	"github.com/go-sql-driver/mysql"
	"github.com/vmihailenco/redis"
	"time"
)

type Bans struct {
	users   map[Userid]time.Time
	ips     map[string]time.Time
	userips map[Userid][]string
}

type ipBan struct {
	ip     string
	banned chan bool
}

type banIp struct {
	userid Userid
	ip     string
	t      time.Time
}

type useridBan struct {
	userid Userid
	banned chan bool
}

type useridTime struct {
	userid Userid
	t      time.Time
}

var (
	bans         Bans
	ipbanned     = make(chan *ipBan)
	banip        = make(chan *banIp)
	useridbanned = make(chan *useridBan)
	banuserid    = make(chan *useridTime)
	unbanuserid  = make(chan Userid)
)

func initBans() {
	bans = Bans{
		make(map[Userid]time.Time),
		make(map[string]time.Time),
		make(map[Userid][]string),
	}

	go (func() {
		loadActiveBans()
		refreshban := setupRefreshBans()
		ct := time.NewTicker(CLEANMUTESBANSPERIOD)
		for {
			select {
			case <-ct.C:
				cleanBans()
			case uid := <-unbanuserid:
				delete(bans.users, uid)
				for _, ip := range bans.userips[uid] {
					delete(bans.ips, ip)
					D("Unbanned IP: ", ip, "for userid:", uid)
				}
				bans.userips[uid] = nil
			case d := <-banuserid:
				bans.users[d.userid] = d.t
			case d := <-useridbanned:
				if t, ok := bans.users[d.userid]; ok {
					d.banned <- t.After(time.Now().UTC())
				} else {
					d.banned <- false
				}
			case d := <-banip:
				bans.ips[d.ip] = d.t
				if _, ok := bans.userips[d.userid]; !ok {
					bans.userips[d.userid] = make([]string, 0, 1)
				}
				bans.userips[d.userid] = append(bans.userips[d.userid], d.ip)
			case d := <-ipbanned:
				if t, ok := bans.ips[d.ip]; ok {
					d.banned <- t.After(time.Now().UTC())
				} else {
					d.banned <- false
				}
			case m := <-refreshban:
				if m.Err != nil {
					D("Error receivong from redis pub/sub channel refreshbans")
					refreshban = setupRefreshBans()
				} else {
					D("Refreshing bans")
					loadActiveBans()
				}
			}
		}
	})()

}

func setupRefreshBans() chan *redis.Message {
	c, err := rds.PubSubClient()
	if err != nil {
		B("Unable to create redis pubsub client: ", err)
		time.Sleep(500 * time.Millisecond)
		return setupRefreshBans()
	}
	refreshban, err := c.Subscribe("refreshbans")
	if err != nil {
		B("Unable to subscribe to the redis refreshbans channel: ", err)
		time.Sleep(500 * time.Millisecond)
		return setupRefreshBans()
	}

	return refreshban
}

func cleanBans() {
	delcount := 0
	for userid, unbantime := range bans.users {
		if unbantime.Before(time.Now().UTC()) {
			delete(bans.users, userid)
			bans.userips[userid] = nil
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
}

func banUser(userid Userid, targetuserid Userid, ban *BanIn) {
	var expiretime time.Time

	if ban.Ispermanent {
		expiretime = time.Now().UTC().AddDate(10, 0, 0) // 10 years from now should be enough
	} else {
		expiretime = time.Now().UTC().Add(time.Duration(ban.Duration))
	}

	banuserid <- &useridTime{targetuserid, expiretime}
	logBan(userid, targetuserid, ban, "")

	if ban.BanIP {
		ips := hub.getIPsForUserid(targetuserid)
		if len(ips) == 0 {
			D("No ips found for user", targetuserid)
		}
		for _, ip := range ips {
			banip <- &banIp{targetuserid, ip, expiretime}
			hub.ipbans <- ip
			logBan(userid, targetuserid, ban, ip)
			D("IPBanned user", ban.Nick, targetuserid, "with ip:", ip)
		}
	}

	hub.bans <- targetuserid
	D("Banned user", ban.Nick, targetuserid)
}

func unbanUserid(userid Userid) {
	logUnban(userid)
	unbanuserid <- userid
	D("Unbanned userid: ", userid)
}

func isUseridIPBanned(ip string, uid Userid) bool {
	c := make(chan bool, 1)
	ipbanned <- &ipBan{ip, c}
	res := <-c
	if res || uid == 0 {
		return res
	}

	useridbanned <- &useridBan{uid, c}
	res = <-c
	return res
}

func loadActiveBans() {
	// purge all the bans
	bans.users = make(map[Userid]time.Time)
	bans.ips = make(map[string]time.Time)
	bans.userips = make(map[Userid][]string)

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
			bans.ips[ipaddress.String] = endtimestamp.Time
			if _, ok := bans.userips[uid]; !ok {
				bans.userips[uid] = make([]string, 1)
			}
			bans.userips[uid] = append(bans.userips[uid], ipaddress.String)
			hub.ipbans <- ipaddress.String
		} else {
			bans.users[uid] = endtimestamp.Time
		}

	}
}

func logBan(userid Userid, targetuserid Userid, ban *BanIn, ip string) {

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

func logUnban(targetuserid Userid) {
	_, err := unbanstatement.Exec(targetuserid)

	if err != nil {
		D("Unable to insert ban: ", err)
	}
}
