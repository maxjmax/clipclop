package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/xgb/xfixes"
	"github.com/BurntSushi/xgb/xproto"
)

var selectedClip *Clip
var x *X

var history *History
var logger *log.Logger

func usage() {
	fmt.Fprint(
		flag.CommandLine.Output(),
		`Usage: clipclip [ARGUMENTS]

clipclop is a clipboard managment daemon. It listens for changes to the X 
selection and stores them in a ring buffer. Selections are not persisted to disk
	
Arguments:
`)
	flag.PrintDefaults()
	fmt.Fprint(
		flag.CommandLine.Output(),
		`	
You can interact with clipclop using the specified unix socket.
The available commands are:

    GET        Get a \n separated list of clips, prefixed with their relative
               time. This is formatted to be fed to dmenu or equivalent.
    SEL [clip] Retrieve the raw clip corresponding to the chosen line (as 
               returned by dmenu or equivalent)
	
For an example of how to use this with dmenu, see clip.sh in the clipclop repo.
`)
}

func main() {
	var err error
	var sock string
	var historySize int

	flag.Usage = usage
	flag.StringVar(&sock, "socket", "/tmp/clipclop.sock", "location of the socket file")
	flag.IntVar(&historySize, "n", 100, "Number of records to keep in history")
	flag.Parse()

	logger = log.New(os.Stdout, "", log.Lshortfile|log.Ldate|log.Ltime)
	history = newHistory(historySize)

	x, err = startX()
	if err != nil {
		logger.Printf("Error starting X: %s", err)
	}

	err = x.CreateEventWindow()
	if err != nil {
		logger.Printf("Error creating event window: %s", err)
	}
	logger.Print("Listening for X events")

	go ipcServer(sock)

	processEvents()
}

func processEvents() {
	for {
		ev, xerr := x.NextEvent()
		if ev == nil && xerr == nil {
			logger.Fatal("Wait for event failed")
			return
		}

		if ev != nil {
			switch ev.(type) {
			case xfixes.SelectionNotifyEvent:
				err := x.ConvertSelection(ev.(xfixes.SelectionNotifyEvent))
				if err != nil {
					logger.Fatalf("Failed to convert selection: %s", err)
				}
			case xproto.SelectionNotifyEvent:
				err, data, format := x.GetSelection(ev.(xproto.SelectionNotifyEvent))
				if err != nil {
					logger.Fatalf("Failed to get selection: %s", err)
				}
				if data != nil {
					// We got a selection
					history.Append(Clip{time.Now(), data, format, "unknown"})
				}

			case xproto.SelectionRequestEvent:
				// Let the requestor know what target is availble for the current clip
				if selectedClip == nil {
					selectedClip = history.Top()
				}
				if selectedClip == nil {
					logger.Print("Nothing in history to share")
				}
				err := x.SetSelection(ev.(xproto.SelectionRequestEvent), &selectedClip.value, selectedClip.format)
				if err != nil {
					logger.Fatalf("Could not set selection for requestor: %s", err)
				}
			case xproto.SelectionClearEvent:
				// Something else has taken ownership, that's fine.

			default:
				logger.Printf("Unknown Event: %s\n", ev)
			}
			// TODO: if fmtid is INCRID then we need extra logic for that
		}
		if xerr != nil {
			logger.Printf("Error waiting for event: %s\n", xerr)
		}
	}
}

func handleCommand(cmd string) string {
	if len(cmd) < 3 {
		return "ERR Invalid command" // commands are 3 characters
	}
	switch cmd[:3] {
	case "GET":
		return strings.Join(history.Format(HistoryFormatter), "\n") + "\n"
	case "SEL":
		clip, err := history.FindEntry(cmd[3:])
		if err != nil {
			return fmt.Sprintf("ERR Not found: %s", err)
		}

		selectedClip = clip
		err = x.BecomeSelectionOwner()
		if err != nil {
			return fmt.Sprintf("ERR Could not become owner: %s", err)
		}
		return "OK"
	default:
		return "ERR Unknown command"
	}
}

func ipcServer(sock string) {
	if err := os.RemoveAll(sock); err != nil {
		logger.Fatalf("Could not remove IPC socket file %s", sock)
	}

	listener, err := net.Listen("unix", sock)
	if err != nil {
		logger.Fatalf("Could not listen on %s: %s", sock, err)
	}
	logger.Printf("Listening on socket %s", sock)

	for {
		conn, err := listener.Accept()
		if err != nil {
			logger.Fatal("Could not accept connection ", err)
		}

		cmd, err := ioutil.ReadAll(conn)
		if err != nil {
			logger.Print("Could not accept connection ", err)
		} else {
			output := handleCommand(string(cmd))
			conn.Write([]byte(output))
		}

		conn.Close()
	}
}
