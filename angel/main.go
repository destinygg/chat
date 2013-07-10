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

	base := path.Base(binpath)
	basedir := strings.TrimSuffix(binpath, "/"+base)
	os.Chdir(basedir) // so that the chat can still read the settings.cfg

	shouldrestart := make(chan bool)
	processexited := make(chan bool)
	sct := make(chan os.Signal, 1)
	signal.Notify(sct, syscall.SIGTERM)

	go (func() {
		for _ = range sct {
			P("CAUGHT SIGTERM, restarting the chat")
			shouldrestart <- true
		}
	})()

again:
	cmd := exec.Command(binpath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	laststarttime := time.Now()
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

	time.Sleep(1 * time.Second)
	go checkResponse(serverurl, origin, shouldrestart)
	t := time.NewTicker(500 * time.Millisecond)

	for {
		select {
		case <-t.C:
			go checkNames(serverurl, origin, shouldrestart)
		case <-shouldrestart:
			cmd.Process.Signal(syscall.SIGINT)
			// TODO move pprof files out of the dir
		case <-processexited:
			if time.Now().Sub(laststarttime) < 5*time.Second {
				P("Tried restarting the chat process too fast, sleeping for 5 seconds")
				time.Sleep(5 * time.Second)
			}
			goto again
		}
	}
}

func checkResponse(serverurl, origin string, shouldrestart chan bool) {
	ws, err := websocket.Dial(serverurl, "", origin)
	if err != nil {
		D("Unable to connect to ", serverurl)
		shouldrestart <- true
		return
	}

	defer ws.Close()

	buff := make([]byte, 512)
	for {
		// the pinginterval is 10 seconds on the server side, we HAVE TO receive something
		ws.SetReadDeadline(time.Now().Add(25 * time.Second))
		len, err := ws.Read(buff)
		if err != nil {
			D("Unable to read from the websocket", err)
			shouldrestart <- true
			return
		}

		D("checkResponse received:", string(buff[:len]))

		if string(buff[:4]) == "PING" {
			ws.SetWriteDeadline(time.Now().Add(time.Second))
			buff[1] = 'O'
			_, err = ws.Write(buff[:len])
			if err != nil {
				P("Unable to write to the websocket", err)
				shouldrestart <- true
				return
			}
		}
	}
}

func checkNames(serverurl, origin string, shouldrestart chan bool) {
	ws, err := websocket.Dial(serverurl, "", origin)
	if err != nil {
		D("Unable to connect to ", serverurl)
		shouldrestart <- true
		return
	}

	defer ws.Close()

	buff := make([]byte, 512)
	ws.SetReadDeadline(time.Now().Add(time.Second))
	_, err = ws.Read(buff)
	if err != nil {
		D("Unable to read from the websocket ", err)
		shouldrestart <- true
		return
	}

	if string(buff[:5]) != "NAMES" && string(buff[:4]) != "JOIN" {
		P("Unexpected data received: ", string(buff))
		shouldrestart <- true
		return
	}

	D("checkNames received NAMES or JOIN, exiting, data was:", string(buff))
}
