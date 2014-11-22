package main

import (
	"encoding/json"
	"fmt"
	redis "github.com/vmihailenco/redis"
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
	if ip == "127.0.0.1" {
		return
	}
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

func initBroadcast(redisdb int64) {
	go setupBroadcast(redisdb)
}

func setupBroadcast(redisdb int64) {
	broadcast := getBroadcastChan(redisdb)

	for {
		select {
		case msg := <-broadcast:
			if msg.Err != nil {
				D("Error receiving from redis pub/sub channel broadcast")
				broadcast = getBroadcastChan(redisdb)
				continue
			}
			if len(msg.Message) == 0 { // a spurious message, ignore
				continue
			}

			var bc EventDataIn
			err := json.Unmarshal([]byte(msg.Message), &bc)
			if err != nil {
				D("unable to unmarshal broadcast message", msg.Message)
				continue
			}

			data := &EventDataOut{}
			data.Timestamp = unixMilliTime()
			data.Data = bc.Data
			m, _ := Marshal(data)
			hub.broadcast <- &message{
				event: "BROADCAST",
				data:  m,
			}
			db.insertChatEvent(Userid(0), "BROADCAST", data)
		}
	}
}

func getBroadcastChan(redisdb int64) chan *redis.Message {
broadcastagain:
	c, err := rds.PubSubClient()
	if err != nil {
		B("Unable to create redis pubsub client: ", err)
		time.Sleep(500 * time.Millisecond)
		goto broadcastagain
	}
	broadcast, err := c.Subscribe(fmt.Sprintf("broadcast-%d", redisdb))
	if err != nil {
		B("Unable to subscribe to the redis broadcast channel: ", err)
		time.Sleep(500 * time.Millisecond)
		goto broadcastagain
	}

	return broadcast
}
