package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/BurntSushi/xgb/xfixes"
	"github.com/BurntSushi/xgb/xproto"
	"github.com/maxjmax/clipclop/history"
	"github.com/maxjmax/clipclop/ipc"
	"github.com/maxjmax/clipclop/x"
)

func usage() {
	fmt.Fprint(
		flag.CommandLine.Output(),
		`Usage: clipclip [ARGUMENTS]

clipclop is a clipboard management daemon. It listens for changes to the X 
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

type options struct {
	Sock        string
	HistorySize int
	Debug       bool
	MinClipSize int
}

func main() {
	var opts options
	flag.Usage = usage
	flag.StringVar(&opts.Sock, "socket", "/tmp/clipclop.sock", "location of the socket file")
	flag.IntVar(&opts.HistorySize, "n", 100, "Number of records to keep in history")
	flag.BoolVar(&opts.Debug, "v", false, "Print verbose debugging output")
	flag.IntVar(&opts.MinClipSize, "m", 4, "Min clip size. Smaller clips will be discarded.")
	flag.Parse()
	logger := log.New(os.Stdout, "", log.Lshortfile|log.Ldate|log.Ltime)

	run(logger, opts)
}

func run(logger *log.Logger, opts options) {
	var err error

	hist := history.NewHistory(opts.HistorySize)
	xconn, err := x.StartX()
	if err != nil {
		logger.Fatalf("Error starting X: %s", err)
	}

	err = xconn.CreateEventWindow()
	if err != nil {
		logger.Fatalf("Error creating event window: %s", err)
	}
	logger.Print("Listening for X events")

	go ipc.IPCServer(logger, hist, xconn, opts.Sock)
	processEvents(logger, hist, xconn, opts)
}

func processEvents(logger *log.Logger, hist *history.History, xconn *x.X, opts options) {
	for {
		ev, xerr := xconn.NextEvent()
		if ev == nil && xerr == nil {
			logger.Fatal("Wait for event failed")
			return
		}
		if xerr != nil {
			logger.Printf("Error waiting for event: %s\n", xerr)
			continue
		}
		if ev == nil {
			continue
		}

		if opts.Debug {
			logger.Println(xconn.DumpEvent(&ev))
		}

		switch ev := ev.(type) {
		case xfixes.SelectionNotifyEvent:
			err := xconn.ConvertSelection(ev)
			if err != nil {
				logger.Printf("Failed to convert selection: %s", err)
			}

		case xproto.SelectionNotifyEvent:
			data, format, err := xconn.GetSelection(ev)
			if err != nil {
				logger.Printf("Failed to get selection: %s", err)
			}
			if data != nil && len(data) >= opts.MinClipSize {
				// We got a selection
				hist.Append(history.Clip{Created: time.Now(), Value: data, Format: format, Source: "unknown"})
			}

		case xproto.SelectionRequestEvent:
			// Let the requestor know what target is available for the current clip
			selectedClip := hist.GetSelected()

			if selectedClip == nil {
				logger.Print("Nothing in history to share")
			} else {
				err := xconn.SetSelection(ev, &selectedClip.Value, selectedClip.Format)
				if err != nil {
					logger.Printf("could not set selection for requestor: %s", err)
				}
			}
		case xproto.SelectionClearEvent:
			// Something else has taken ownership

		case xproto.PropertyNotifyEvent:
			// During INCR, we listen for DELETEs
			if !xconn.IsEventWindow(ev.Window) && ev.State == xproto.PropertyDelete {
				err := xconn.ContinueSetSelection(ev)
				if err != nil {
					logger.Printf("error during INCR set selection: %s", err)
				}
			}

		default:
			logger.Printf("Unknown Event: %s\n", ev)
		}
		// TODO: if fmtid is INCRID then we need extra logic for that
	}
}
