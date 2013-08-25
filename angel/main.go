package main

import (
	"code.google.com/p/go.net/websocket"
	conf "github.com/msbranco/goconfig"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"
)

var debuggingenabled bool

func main() {
	c, err := conf.ReadConfigFile("angel.cfg")
	if err != nil {
		nc := conf.NewConfigFile()
		nc.AddOption("default", "debug", "false")
		nc.AddOption("default", "binarypath", "./")
		nc.AddOption("default", "serverurl", "ws://localhost:9998/ws")
		nc.AddOption("default", "origin", "http://localhost")

		if err := nc.WriteConfigFile("angel.cfg", 0644, "Chat Angel, watching over the chat and restarting it as needed"); err != nil {
			log.Fatal("Unable to create angel.cfg: ", err)
		}
		if c, err = conf.ReadConfigFile("angel.cfg"); err != nil {
			log.Fatal("Unable to read angel.cfg: ", err)
		}
	}

	debuggingenabled, _ = c.GetBool("default", "debug")
	binpath, _ := c.GetString("default", "binarypath")
	serverurl, _ := c.GetString("default", "serverurl")
	origin, _ := c.GetString("default", "origin")
	initLog()

	base := path.Base(binpath)
	basedir := strings.TrimSuffix(binpath, "/"+base)
	os.Chdir(basedir) // so that the chat can still read the settings.cfg

	shouldrestart := make(chan bool)
	processexited := make(chan bool)
	t := time.NewTicker(2 * time.Second)
	sct := make(chan os.Signal, 1)
	signal.Notify(sct, syscall.SIGTERM)

	go (func() {
		for _ = range sct {
			P("CAUGHT SIGTERM, restarting the chat")
			shouldrestart <- true
		}
	})()

	var restarting bool

again:
	initLogWriter()
	cmd := exec.Command(binpath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		F("Stdoutpipe error: ", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		F("Stderrpipe error: ", err)
	}

	go accumulateLog(stdout)
	go accumulateLog(stderr)

	if err := cmd.Start(); err != nil {
		P("Error starting", binpath, err)
		return
	}

	go (func() {
		if err := cmd.Wait(); err != nil {
			P("Error while waiting for process to exit ", err)
		}
		P("Chat process exited, restarting")
		processexited <- true
	})()

	time.Sleep(2 * time.Second)
	restarting = false

	for {
		select {
		case <-t.C:
			go checkNames(serverurl, origin, shouldrestart)
		case <-shouldrestart:
			if !restarting {
				cmd.Process.Signal(syscall.SIGQUIT)
			} else {
				P("Received from shouldrestart but already restarting, ignored")
			}
			// TODO move pprof files out of the dir
		case <-processexited:
			if !restarting {
				time.Sleep(200 * time.Millisecond)
				goto again
			}
		}
	}
}

func checkNames(serverurl, origin string, shouldrestart chan bool) {
	ws, err := websocket.Dial(serverurl, "", origin)
	if err != nil {
		P("Unable to connect to ", serverurl)
		shouldrestart <- true
		return
	}

	defer ws.Close()
	buff := make([]byte, 512)
	start := time.Now()

checkagain:
	ws.SetReadDeadline(time.Now().Add(time.Second))
	_, err = ws.Read(buff)
	if err != nil {
		P("Unable to read from the websocket ", err)
		shouldrestart <- true
		return
	}

	if time.Since(start) > 500*time.Millisecond {
		P("Didnt receive NAMES in 500ms, restarting")
		shouldrestart <- true
		return
	}

	if string(buff[:5]) != "NAMES" {
		goto checkagain
	}

}
