package main

import (
	"fmt"
	"github.com/vmihailenco/redis"
	"time"
)

var (
	rds               *redis.Client
	rdsCircularBuffer string
	rdsGetIPCache     string
	rdsSetIPCache     string
)

func initRedis(addr string, db int64, pw string) {
	rds = redis.NewTCPClient(addr, pw, db)

	ret := rds.ScriptLoad(`
		local key, value, maxlength = KEYS[1], ARGV[1], tonumber(ARGV[2])
		if not maxlength then
			return {err = "INVALID ARGUMENTS"}
		end
		
		redis.call("RPUSH", key, value)
		local listlength = redis.call("LLEN", key)
		
		if listlength >= maxlength then
			local start = listlength - maxlength
			redis.call("LTRIM", key, start, maxlength)
		end
	`)
	rdsCircularBuffer = ret.Val()

	ret = rds.ScriptLoad(`
		local key = KEYS[1]
		return redis.call("ZRANGEBYSCORE", key, 1, 3)
	`)
	rdsGetIPCache = ret.Val()

	ret = rds.ScriptLoad(`
		local key, value, maxlength = KEYS[1], ARGV[1], 3
		
		local count = redis.call("ZCOUNT", key, 1, maxlength)
		local existingscore = redis.call("ZSCORE", key, value)
		if existingscore then
			-- renumber all the elements and make this one the last
			local elements = redis.call("ZRANGEBYSCORE", key, 1, maxlength)
			local i = 1
			for _, v in ipairs(elements) do
				if v == value then
					redis.call("ZADD", key, count, v)
				else
					redis.call("ZADD", key, i, v)
					i = i + 1
				end
			end
			return
		end
		
		if count == maxlength then
			-- delete the first element, modify the other elements score down
			-- and add the new one to the end
			redis.call("ZREMRANGEBYSCORE", key, 1, 1)
			local elements = redis.call("ZRANGEBYSCORE", key, 2, maxlength)
			local i = 1
			for _, v in ipairs(elements) do
				redis.call("ZADD", key, i, v)
				i = i + 1
			end
			return redis.call("ZADD", key, count, value)
		else
			-- otherwise just insert it with the next score
			return redis.call("ZADD", key, count + 1, value)
		end
	`)
	rdsSetIPCache = ret.Val()

	go (func() {
		t := time.NewTicker(time.Minute)
		cp := watchdog.register("redis check thread", time.Minute)
		defer watchdog.unregister("redis check thread")

		for {
			select {
			case <-t.C:
				cp <- true
				ping := rds.Ping()
				if ping.Err() != nil || ping.Val() != "PONG" {
					D("Could not ping redis: ", ping.Err())
					rds.Close()
					initRedis(addr, db, pw)
					return
				}
			}
		}
	})()

}

func cacheIPForUser(userid Userid, ip string) {
	key := []string{fmt.Sprintf("CHAT:userips-%d", userid)}
	rds.EvalSha(rdsSetIPCache, key, []string{ip})
}

func getIPCacheForUser(userid Userid) []string {
	key := []string{fmt.Sprintf("CHAT:userips-%d", userid)}
	res := rds.EvalSha(rdsGetIPCache, key, []string{})
	if res.Err() != nil {
		return []string{}
	}

	ips := make([]string, 0)
	for _, v := range res.Val().([]interface{}) {
		ips = append(ips, v.(string))
	}

	return ips
}
