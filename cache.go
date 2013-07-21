package main

import (
	"github.com/vmihailenco/redis"
	"time"
)

var rds *redis.Client
var rdsCircularBufferHash string

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
	rdsCircularBufferHash = ret.Val()

	go (func() {
		t := time.NewTicker(time.Minute)
		for {
			select {
			case <-t.C:
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
