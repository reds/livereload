package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/exp/inotify"
	"golang.org/x/net/websocket"
)

// <script src="http://localhost:35729/livereload.js"></script>

type llcmd struct {
	Command    string   `json:"command"`
	Protocols  []string `json:"protocols,omitempty"`
	Url        string   `json:"url,omitempty"`
	Path       string   `json:"path,omitempty"`
	LiveCSS    bool     `json:"liveCSS"`
	Message    string   `json:"message,omitempty"`
	ServerName string   `json:"serverName,omitempty"`
}

const hostPort = ":35729"

type fncCmd int

const (
	fncCmdQuit fncCmd = iota
	fncCmdFileChanged
	fncCmdAddWS
	fncCmdRemWS
	fncCmdAdmin
)

type fncMsg struct {
	cmd   fncCmd
	path  string
	forWS *websocket.Conn
}

func liveloadHandler(ws *websocket.Conn, fncChan chan fncMsg) {
	log.Println("serveing")
	var msg llcmd
	err := websocket.JSON.Receive(ws, &msg)
	if err != nil {
		log.Println(err)
		return
	}
	err = websocket.JSON.Send(ws,
		&llcmd{
			Command:    "hello",
			Protocols:  []string{"http://livereload.com/protocols/official-7"},
			ServerName: "goLivereload",
		})
	if err != nil {
		log.Println(err)
		return
	}
	for {
		err := websocket.JSON.Receive(ws, &msg)
		if err != nil {
			log.Println(err)
			fncChan <- fncMsg{cmd: fncCmdQuit}
			return
		}
		log.Println(msg)
	}
}

func skipPath(path string, info os.FileInfo) bool {
	if info.IsDir() {
		return true
	}
	if strings.HasPrefix(info.Name(), ".") {
		return true
	}
	if strings.HasPrefix(info.Name(), "#") {
		return true
	}
	if strings.HasSuffix(info.Name(), "~") {
		return true
	}
	return false
}

func watchFilesDarwin(top string, c chan fncMsg) {
	modTimes := make(map[string]time.Time)
	filepath.Walk(top, func(path string, info os.FileInfo, err error) error {
		if skipPath(path, info) {
			return nil
		}
		modTimes[path] = info.ModTime()
		return nil
	})
	for {
		filepath.Walk(top, func(path string, info os.FileInfo, err error) error {
			if skipPath(path, info) {
				return nil
			}
			if ts, exists := modTimes[path]; exists {
				if info.ModTime().After(ts) {
					fmt.Println("changed", path, info)
					modTimes[path] = info.ModTime()
					c <- fncMsg{cmd: fncCmdFileChanged, path: path}
					return nil
				}
			} else {
				// new file
				fmt.Println("new file", path, info)
				modTimes[path] = info.ModTime()
				c <- fncMsg{cmd: fncCmdFileChanged, path: path}
				return nil
			}
			return nil
		})
		time.Sleep(time.Second)
	}
}

func watchFilesUnix(top string, c chan fncMsg) {
	watcher, err := inotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	err = watcher.Watch(top)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("watching", top)
	for {
		log.Println("event")
		select {
		case ev := <-watcher.Event:
			if ev.Mask&inotify.IN_MODIFY == inotify.IN_MODIFY {
				c <- fncMsg{cmd: fncCmdFileChanged, path: ev.Name[11:]}
			}
		case err := <-watcher.Error:
			log.Println("error:", err)
		}
	}
}

func doUpdates(c chan fncMsg) {
	allWS := make(map[*websocket.Conn]error)
	for fn := range c {
		fmt.Println("fncChan", fn)
		switch fn.cmd {
		case fncCmdAddWS:
			allWS[fn.forWS] = nil
		case fncCmdRemWS:
			delete(allWS, fn.forWS)
		case fncCmdFileChanged:
			fmt.Println("reload", fn.path)
			for ws, wsErr := range allWS {
				if wsErr != nil {
					continue
				}
				err := websocket.JSON.Send(ws,
					&llcmd{
						Command: "reload",
						Path:    fn.path,
						LiveCSS: true,
					})
				if err != nil {
					allWS[ws] = err
					log.Println(err)
				}
			}
		case fncCmdAdmin:
			newAllWS := make(map[*websocket.Conn]error)
			for ws, err := range allWS {
				if err == nil {
					newAllWS[ws] = nil
				}
			}
			allWS = newAllWS
		}
	}
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	fncChan := make(chan fncMsg)
	go doUpdates(fncChan)
	go watchFilesDarwin("dippapp/doc", fncChan)
	http.Handle("/", http.FileServer(http.Dir("doc")))
	http.Handle("/livereload", websocket.Handler(func(ws *websocket.Conn) {
		fncChan <- fncMsg{cmd: fncCmdAddWS, forWS: ws}
		liveloadHandler(ws, fncChan)
		fncChan <- fncMsg{cmd: fncCmdRemWS, forWS: ws}
	}))
	err := http.ListenAndServe(hostPort, nil)
	if err != nil {
		panic("ListenAndServe: " + err.Error())
	}
}
