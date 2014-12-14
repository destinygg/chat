package main

import (
	"fmt"
	"time"

	"github.com/tideland/godm/v3/redis"
)

var (
	rds               *redis.Database
	rdsCircularBuffer string
	rdsGetIPCache     string
	rdsSetIPCache     string
)

func redisGetConn() *redis.Connection {
again:
	conn, err := rds.Connection()
	if err != nil {
		D("Error getting a redis connection", err)
		if conn != nil {
			conn.Return()
		}
		time.Sleep(500 * time.Millisecond)
		goto again
	}

	return conn
}

func initRedis(addr string, db int64, pw string) {
	var err error
	rds, err = redis.Open(
		redis.TcpConnection(addr, 1*time.Second),
		redis.Index(int(db), pw),
		redis.PoolSize(50),
	)
	if err != nil {
		F("Error making the redis pool", err)
	}

	conn := redisGetConn()
	defer conn.Return()

	rdsCircularBuffer, err = conn.DoString("SCRIPT LOAD", `
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
	if err != nil {
		F("Circular buffer script loading error", err)
	}

	rdsGetIPCache, err = conn.DoString("SCRIPT LOAD", `
		local key = KEYS[1]
		return redis.call("ZRANGEBYSCORE", key, 1, 3)
	`)
	if err != nil {
		F("Get IP Cache script loading error", err)
	}

	rdsSetIPCache, err = conn.DoString("SCRIPT LOAD", `
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
	if err != nil {
		F("Set IP Cache script loading error", err)
	}
}

func cacheIPForUser(userid Userid, ip string) {
	if ip == "127.0.0.1" {
		return
	}

	conn := redisGetConn()
	defer conn.Return()

	_, err := conn.Do("EVALSHA", rdsSetIPCache, fmt.Sprintf("CHAT:userips-%d", userid))
	if err != nil {
		D("cacheIPForUser redis error", err)
	}
}

func getIPCacheForUser(userid Userid) []string {
	conn := redisGetConn()
	defer conn.Return()

	ips, err := conn.DoStrings("EVALSHA", rdsGetIPCache, fmt.Sprintf("CHAT:userips-%d", userid))
	if err != nil {
		D("getIPCacheForUser redis error", err)
	}

	return ips
}

func isSubErr(sub *redis.Subscription, err error) bool {
	if err != nil {
		D("Getting a subscription failed with error", err)
		if sub != nil {
			sub.Close()
		}
		time.Sleep(500 * time.Millisecond)
		return true
	}
	return false
}

func setupRedisSubscription(channel string, redisdb int64, cb func(*redis.PublishedValue)) {
again:
	sub, err := rds.Subscription()
	if isSubErr(sub, err) {
		goto again
	}

	err = sub.Subscribe(fmt.Sprintf("%s-%d", channel, redisdb))
	if isSubErr(sub, err) {
		goto again
	}

	for {
		result, err := sub.Pop()
		if isSubErr(sub, err) {
			goto again
		}

		if result.Value.IsNil() {
			continue
		}

		D(result)
		cb(result)
	}
}

func redisGetBytes(key string) ([]byte, error) {
	conn := redisGetConn()
	defer conn.Return()

	result, err := conn.Do("GET", key)
	if err != nil {
		return []byte{}, err
	}

	value, err := result.ValueAt(0)
	if err != nil {
		return []byte{}, err
	}

	return value.Bytes(), err
}
