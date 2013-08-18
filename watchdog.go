package main

import (
	"time"
)

type wdog struct {
	name       string
	duration   time.Duration
	c          chan bool
	t          chan time.Time
	expectedat time.Time
}

type watchDog struct {
	watchdogs   map[string]*wdog
	newwatchdog chan *wdog
	delwatchdog chan string
}

var watchdog = watchDog{
	watchdogs:   make(map[string]*wdog),
	newwatchdog: make(chan *wdog),
	delwatchdog: make(chan string),
}

func initWatchdog() {
	go watchdog.run()
}

func (w *watchDog) run() {
	p := time.NewTicker(WATCHDOGINTERVAL)
	t := time.NewTicker(WATCHDOGINTERVAL + 200*time.Millisecond)
	for {
		select {
		case p := <-p.C:
			for _, wa := range w.watchdogs {
				wa.t <- p
			}
		case wa := <-w.newwatchdog:
			w.watchdogs[wa.name] = wa
		case name := <-w.delwatchdog:
			delete(w.watchdogs, name)
		case now := <-t.C:
			for name, wa := range w.watchdogs {
				if now.After(wa.expectedat) {
					if len(wa.c) == 0 {
						F("Watchdog didn't receive anything from ", name, " in time! Restarting...")
					}
					wa.expectedat = now.Add(wa.duration - 200*time.Millisecond)
					if len(wa.c) > 0 {
						<-wa.c
					}
				}
			}
		}
	}
}

func (w *watchDog) register(name string) (chan time.Time, chan bool) {
	p := make(chan time.Time, 4)
	c := make(chan bool, 4)
	watchdog.newwatchdog <- &wdog{name, WATCHDOGINTERVAL, c, p, time.Now().Add(WATCHDOGINTERVAL)}
	return p, c
}

func (w *watchDog) unregister(name string) {
	watchdog.delwatchdog <- name
}
