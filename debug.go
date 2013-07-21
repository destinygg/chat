package main

import (
	"errors"
	"fmt"
	//"github.com/emicklei/hopwatch"
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

func F(v ...interface{}) {
	ts := time.Now().Format("2006-02-01 15:04:05: ")
	println(ts, NewErrorTrace(v...).Error())
	panic("-----")
}

func D(v ...interface{}) {
	if debuggingenabled {
		log.Println(v...)
	}
}

func DP(v ...interface{}) {
	if debuggingenabled {
		log.Print(v...)
	}
}

func P(v ...interface{}) {
	log.Println(v...)
}
