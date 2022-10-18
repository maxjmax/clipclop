package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/BurntSushi/xgb"
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

Example:

  clipclop -n 200 -preset "useful command" -preset "020 7898 1000" -socket /tmp/s.sock -v -m 6 &
`)
}

type flagArray []string

func (a *flagArray) String() string {
	return strings.Join(*a, "\n")
}

func (a *flagArray) Set(value string) error {
	*a = append(*a, value)
	return nil
}

type options struct {
	Sock        string
	HistorySize int
	Debug       bool
	MinClipSize int
	Presets     flagArray
}

func main() {
	var opts options
	flag.Usage = usage
	flag.StringVar(&opts.Sock, "socket", "/tmp/clipclop.sock", "location of the socket file")
	flag.IntVar(&opts.HistorySize, "n", 100, "Number of records to keep in history")
	flag.BoolVar(&opts.Debug, "v", false, "Print verbose debugging output")
	flag.IntVar(&opts.MinClipSize, "m", 4, "Min clip size. Smaller clips will be discarded.")
	flag.Var(&opts.Presets, "preset", "One or more preset strings that will always be included in the history. They will not count towards the history size.")

	flag.Parse()
	logger := log.New(os.Stdout, "", log.Lshortfile|log.Ldate|log.Ltime)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	run(ctx, logger, opts)
}

func run(ctx context.Context, logger *log.Logger, opts options) {
	var err error
	hist := history.NewHistory(opts.HistorySize, []string(opts.Presets))
	xconn, err := x.StartX()
	if err != nil {
		logger.Fatalf("Error starting X: %s", err)
	}

	err = xconn.CreateEventWindow()
	if err != nil {
		logger.Fatalf("Error creating event window: %s", err)
	}
	logger.Print("Listening for X events")

	go ipc.IPCServer(ctx, logger, hist, xconn, opts.Sock)
	processEvents(ctx, logger, hist, xconn, opts)
}

func processEvents(ctx context.Context, logger *log.Logger, hist *history.History, xconn *x.X, opts options) {
	go func() {
		<-ctx.Done()
		logger.Print("Shutting down")
		xconn.Close()
	}()

	for {
		ev, xerr := xconn.NextEvent()
		if ev == nil && xerr == nil {
			// shutting down
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

		handleEvent(ev, logger, hist, xconn, opts)
	}
}

func handleEvent(ev xgb.Event, logger *log.Logger, hist *history.History, xconn *x.X, opts options) {
	captureClip := func(data []byte, format history.ClipFormat) {
		clip := history.Clip{Created: time.Now(), Value: data, Format: format, Source: "unknown"}
		hist.Append(clip)

		// Take the selection so that if someone pastes now, the data comes from us. This avoid the case of someone
		// copying from vim, closing vim, then trying to paste it elsewhere.
		err := xconn.BecomeSelectionOwner()

		hist.SetSelected(&clip)
		if err != nil {
			logger.Printf("Failed to become selection owner after capturing clip: %s", err)
		}
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
			captureClip(data, format)
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
		// TODO: many errors in console during INCR, not stopping listening correctly?
		if ev.State == xproto.PropertyDelete {
			err := xconn.ContinueSetSelection(ev)
			if err != nil {
				logger.Printf("error during INCR set selection: %s", err)
			}
		} else {
			data, format, err := xconn.ContinueGetSelection(ev)
			if err != nil {
				logger.Printf("error during INCR get selection: %s", err)
			} else if data != nil {
				// the INCR is complete
				captureClip(data, format)
			}
		}

	default:
		logger.Printf("Unknown Event: %s\n", ev)
	}
}
