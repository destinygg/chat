package main

import (
	"time"
)

type wdog struct {
	name       string
	duration   time.Duration
	c          chan bool
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
	t := time.NewTicker(time.Minute + 500*time.Millisecond)
	for {
		select {
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
					wa.expectedat = now.Add(wa.duration - 500*time.Millisecond)
					if len(wa.c) > 0 {
						<-wa.c
					}
				}
			}
		}
	}
}

func (w *watchDog) register(name string, duration time.Duration) chan bool {
	c := make(chan bool, 4)
	watchdog.newwatchdog <- &wdog{name, duration, c, time.Now().Add(duration)}
	return c
}

func (w *watchDog) unregister(name string) {
	watchdog.delwatchdog <- name
}
