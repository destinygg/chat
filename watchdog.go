package main

import (
	"time"
)

type watchdog struct {
	name       string
	duration   time.Duration
	c          chan bool
	expectedat time.Time
}

var (
	watchdogs   = make([]*watchdog, 0, 4)
	newwatchdog = make(chan *watchdog)
	delwatchdog = make(chan string)
)

func initWatchdog() {
	go (func() {
		t := time.NewTimer(time.Minute + 500*time.Millisecond)
		for {
			select {
			case w := <-newwatchdog:
				watchdogs = append(watchdogs, w)
			case name := <-delwatchdog:
				for _, w := range watchdogs {
					if w.name == name {
						w.remove()
					}
				}
			case now := <-t.C:
				for _, w := range watchdogs {
					if now.After(w.expectedat) {
						if len(w.c) == 0 {
							F("Watchdog didn't receive anything from ", w.name, " in time! Restarting...")
						}
						w.expectedat = now.Add(w.duration)
						if len(w.c) > 0 {
							<-w.c
						}
					}
				}
			}
		}
	})()
}

func (w *watchdog) remove() {
	// https://code.google.com/p/go-wiki/wiki/SliceTricks
	for i, va := range watchdogs {
		if va == w {
			j := i + 1
			if len(watchdogs)-1 < j {
				// pop
				watchdogs[i] = nil
				watchdogs = watchdogs[:i]
			} else {
				// cut
				copy(watchdogs[i:], watchdogs[j:])
				for k, n := len(watchdogs)-j+i, len(watchdogs); k < n; k++ {
					watchdogs[k] = nil
				}
				watchdogs = watchdogs[:len(watchdogs)-j+i]
			}
		}
	}
}

func registerWatchdog(name string, duration time.Duration) chan bool {

	c := make(chan bool, 2)
	newwatchdog <- &watchdog{name, duration, c, time.Now().Add(duration)}
	return c

}

func unregisterWatchdog(name string) {
	delwatchdog <- name
}
