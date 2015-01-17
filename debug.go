package main

import (
	"errors"
	"fmt"
	//"github.com/emicklei/hopwatch"
	"bytes"
	"log"
	"runtime"
	"time"
)

// source https://groups.google.com/forum/?fromgroups#!topic/golang-nuts/C24fRw8HDmI
// from David Wright
type ErrorTrace struct {
	err   error
	trace string
}

func NewErrorTrace(v ...interface{}) error {
	msg := fmt.Sprint(v...)
	buf := bytes.Buffer{}
	skip := 2
addtrace:
	pc, file, line, ok := runtime.Caller(skip)
	if ok && skip < 6 { // print a max of 6 lines of trace
		fun := runtime.FuncForPC(pc)
		buf.WriteString(fmt.Sprint(fun.Name(), " -- ", file, ":", line, "\n"))
		skip++
		goto addtrace
	}

	if buf.Len() > 0 {
		trace := buf.String()
		return ErrorTrace{err: errors.New(msg), trace: trace}
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

func F(v ...interface{}) {
	ts := time.Now().Format("2006-02-01 15:04:05: ")
	println(ts, NewErrorTrace(v...).Error())
	panic("-----")
}

func D(v ...interface{}) {
	if debuggingenabled {
		formatstring := ""
		for range v {
			formatstring += " %+v"
		}
		log.Printf(formatstring, v...)
	}
}

func DP(v ...interface{}) {
	if debuggingenabled {
		formatstring := ""
		for range v {
			formatstring += " %+v"
		}
		log.Printf(formatstring, v...)
	}
}

func P(v ...interface{}) {
	formatstring := ""
	for range v {
		formatstring += " %+v"
	}
	log.Printf(formatstring, v...)
}
