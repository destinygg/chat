package main

import (
	"errors"
	"fmt"
	//"github.com/emicklei/hopwatch"
	"bufio"
	"bytes"
	"io"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

var (
	logger           *log.Logger
	logfile          *os.File
	accumulatewriter = bytes.NewBufferString("")
	accumulatelock   sync.Mutex
)

func initLog() {
	logger = log.New(accumulatewriter, "angel> ", log.Ldate|log.Ltime)
	go consumeLog()
}

func consumeLog() {
	// only one consumer
	for {
		accumulatelock.Lock()
		if accumulatewriter.Len() <= 0 {
			accumulatelock.Unlock()
			time.Sleep(500 * time.Millisecond)
			continue
		}

		s, _ := accumulatewriter.ReadString('\n')
		s = strings.TrimSpace(s)
		println(s)
		logfile.WriteString(s)
		logfile.WriteString("\n")
		accumulatelock.Unlock()
	}
}

func initLogWriter() {
	defer accumulatelock.Unlock()
	accumulatelock.Lock()
	filename := time.Now().Format("logs/log-20060201-150405.txt")
	if logfile != nil {
		logfile.Close() // do not care about error
	}
	var err error
	logfile, err = os.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		println("logfile creation error: ", err)
	}
}

func accumulateLog(r io.Reader) {
	// multiple
	br := bufio.NewReader(r)
	for {
		l, err := br.ReadString('\n')
		if err != nil {
			return
		}
		l = strings.TrimSpace(l)

		accumulatelock.Lock()
		_, err = accumulatewriter.WriteString(l)
		_, err = accumulatewriter.WriteString("\n")
		accumulatelock.Unlock()

		if err != nil {
			return
		}
	}
}

// source https://groups.google.com/forum/?fromgroups#!topic/golang-nuts/C24fRw8HDmI
// from David Wright
type ErrorTrace struct {
	err   error
	trace string
}

func NewErrorTrace(v ...interface{}) error {
	msg := fmt.Sprint(v...)
	pc, file, line, ok := runtime.Caller(2)
	if ok {
		fun := runtime.FuncForPC(pc)
		loc := fmt.Sprint(fun.Name(), "\n\t", file, ":", line)
		return ErrorTrace{err: errors.New(msg), trace: loc}
	}
	return errors.New("error generating error")
}

func (et ErrorTrace) Error() string {
	return et.err.Error() + "\n  " + et.trace
}

func B(v ...interface{}) {
	ts := time.Now().Format("2006-02-01 15:04:05: ")
	println(ts, NewErrorTrace(v...).Error())
}

func D(v ...interface{}) {
	if debuggingenabled {
		logger.Println(v...)
	}
}

func DP(v ...interface{}) {
	if debuggingenabled {
		logger.Print(v...)
	}
}

func P(v ...interface{}) {
	logger.Println(v...)
}

func F(v ...interface{}) {
	logger.Fatalln(v...)
}
