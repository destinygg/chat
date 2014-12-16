package main

import (
	"database/sql"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
)

type database struct {
	db          *sql.DB
	insertevent chan *dbInsertEvent
	insertban   chan *dbInsertBan
	deleteban   chan *dbDeleteBan
	sync.Mutex
}

type dbInsertEvent struct {
	uid       Userid
	targetuid *sql.NullInt64
	event     string
	data      *sql.NullString
	timestamp time.Time
	retries   uint8
}

type dbInsertBan struct {
	uid       Userid
	targetuid Userid
	ipaddress *sql.NullString
	reason    string
	starttime time.Time
	endtime   *mysql.NullTime
	retries   uint8
}

type dbDeleteBan struct {
	uid Userid
}

var db = &database{
	insertevent: make(chan *dbInsertEvent, 10),
	insertban:   make(chan *dbInsertBan, 10),
	deleteban:   make(chan *dbDeleteBan, 10),
}

func initDatabase(dbtype string, dbdsn string) {
	var err error
	conn, err := sql.Open(dbtype, dbdsn)
	if err != nil {
		B("Could not open database: ", err)
		time.Sleep(time.Second)
		initDatabase(dbtype, dbdsn)
		return
	}
	err = conn.Ping()
	if err != nil {
		B("Could not connect to database: ", err)
		time.Sleep(time.Second)
		initDatabase(dbtype, dbdsn)
		return
	}

	db.db = conn
	go db.runInsertEvent()
	go db.runInsertBan()
	go db.runDeleteBan()
}

func (db *database) getStatement(name string, sql string) *sql.Stmt {
	db.Lock()
	stmt, err := db.db.Prepare(sql)
	db.Unlock()
	if err != nil {
		D("Unable to create", name, "statement:", err)
		time.Sleep(100 * time.Millisecond)
		return db.getStatement(name, sql)
	}
	return stmt
}

func (db *database) getInsertEventStatement() *sql.Stmt {
	return db.getStatement("insertEvent", `
		INSERT INTO chatlog
		SET
			userid       = ?,
			targetuserid = ?,
			event        = ?,
			data         = ?,
			timestamp    = ?
	`)
}

func (db *database) getInsertBanStatement() *sql.Stmt {
	return db.getStatement("insertBan", `
		INSERT INTO bans
		SET
			userid         = ?,
			targetuserid   = ?,
			ipaddress      = ?,
			reason         = ?,
			starttimestamp = ?,
			endtimestamp   = ?
	`)
}

func (db *database) getDeleteBanStatement() *sql.Stmt {
	return db.getStatement("deleteBan", `
		UPDATE bans
		SET endtimestamp = NOW()
		WHERE
			targetuserid = ? AND
			(
				endtimestamp IS NULL OR
				endtimestamp > NOW()
			)
	`)
}

func (db *database) runInsertEvent() {
	t := time.NewTimer(5 * time.Second)
	stmt := db.getInsertEventStatement()
	for {
		select {
		case <-t.C:
			stmt.Close()
			stmt = nil
		case data := <-db.insertevent:
			t.Reset(time.Minute)
			if stmt == nil {
				stmt = db.getInsertEventStatement()
			}
			if data.retries > 2 {
				continue
			}
			db.Lock()
			_, err := stmt.Exec(data.uid, data.targetuid, data.event, data.data, data.timestamp)
			db.Unlock()
			if err != nil {
				data.retries++
				D("Unable to insert event", err)
				go (func() {
					db.insertevent <- data
				})()
			}
		}
	}
}

func (db *database) runInsertBan() {
	t := time.NewTimer(time.Minute)
	stmt := db.getInsertBanStatement()
	for {
		select {
		case <-t.C:
			stmt.Close()
			stmt = nil
		case data := <-db.insertban:
			t.Reset(time.Minute)
			if stmt == nil {
				stmt = db.getInsertBanStatement()
			}
			if data.retries > 2 {
				continue
			}
			db.Lock()
			_, err := stmt.Exec(data.uid, data.targetuid, data.ipaddress, data.reason, data.starttime, data.endtime)
			db.Unlock()
			if err != nil {
				data.retries++
				D("Unable to insert event", err)
				go (func() {
					db.insertban <- data
				})()
			}
		}
	}
}

func (db *database) runDeleteBan() {
	t := time.NewTimer(time.Minute)
	stmt := db.getDeleteBanStatement()
	for {
		select {
		case <-t.C:
			stmt.Close()
			stmt = nil
		case data := <-db.deleteban:
			t.Reset(time.Minute)
			if stmt == nil {
				stmt = db.getDeleteBanStatement()
			}
			db.Lock()
			_, err := stmt.Exec(data.uid)
			db.Unlock()
			if err != nil {
				D("Unable to insert event", err)
				go (func() {
					db.deleteban <- data
				})()
			}
		}
	}
}

func (db *database) insertChatEvent(uid Userid, event string, data *EventDataOut) {

	targetuid := &sql.NullInt64{}
	if data.Targetuserid != 0 {
		targetuid.Int64 = int64(data.Targetuserid)
		targetuid.Valid = true
	}

	d := &sql.NullString{}
	if len(data.Data) != 0 {
		d.String = data.Data
		d.Valid = true
	}

	// the timestamp is milisecond precision
	ts := time.Unix(data.Timestamp/1000, 0).UTC()
	db.insertevent <- &dbInsertEvent{uid, targetuid, event, d, ts, 0}
}

func (db *database) insertBan(uid Userid, targetuid Userid, ban *BanIn, ip string) {

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

	db.insertban <- &dbInsertBan{uid, targetuid, ipaddress, ban.Reason, starttimestamp, endtimestamp, 0}
}

func (db *database) deleteBan(targetuid Userid) {
	db.deleteban <- &dbDeleteBan{targetuid}
}

func (db *database) getBans(f func(Userid, sql.NullString, mysql.NullTime)) {
	db.Lock()
	defer db.Unlock()

	rows, err := db.db.Query(`
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
		D("Unable to get active bans: ", err)
		return
	}

	defer rows.Close()
	for rows.Next() {
		var uid Userid
		var ipaddress sql.NullString
		var endtimestamp mysql.NullTime
		err = rows.Scan(&uid, &ipaddress, &endtimestamp)

		if err != nil {
			D("Unable to scan bans row: ", err)
			continue
		}

		f(uid, ipaddress, endtimestamp)
	}
}

func (db *database) getUser(nick string) (Userid, bool) {

	stmt := db.getStatement("getUser", `
		SELECT
			u.userId,
			IF(IFNULL(f.featureId, 0) >= 1, 1, 0) AS protected
		FROM dfl_users AS u
		LEFT JOIN dfl_users_features AS f ON (
			f.userId = u.userId AND
			featureId = (SELECT featureId FROM dfl_features WHERE featureName IN("protected", "admin") LIMIT 1)
		)
		WHERE u.username = ?
	`)
	db.Lock()
	defer stmt.Close()
	defer db.Unlock()

	var uid int32
	var protected bool
	err := stmt.QueryRow(nick).Scan(&uid, &protected)
	if err != nil {
		D("error looking up", nick, err)
		return 0, false
	}
	return Userid(uid), protected
}
