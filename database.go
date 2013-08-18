package main

import (
	"database/sql"
	_ "github.com/go-sql-driver/mysql"
	"time"
)

var db *sql.DB

func initDatabase(dbtype string, dbdsn string) {
	var err error
	db, err = sql.Open(dbtype, dbdsn)
	err2 := db.Ping()
	if err != nil || err2 != nil {
		B("Could not connect to database: ", err, err2)
		time.Sleep(time.Second)
		initDatabase(dbtype, dbdsn)
		return
	}
	db.SetMaxIdleConns(10) // totally made up value

	go (func() {
		t := time.NewTicker(time.Minute)
		p, cp := watchdog.register("database check thread")
		defer watchdog.unregister("database check thread")

		for {
			select {
			case <-p:
				cp <- true
			case <-t.C:
				err := db.Ping()
				if err != nil {
					B("Could not ping database: ", err)
					initDatabase(dbtype, dbdsn)
					initEventlog()
					return
				}
			}
		}
	})()

}
