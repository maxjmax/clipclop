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

	"github.com/BurntSushi/xgb"
	"github.com/BurntSushi/xgb/xfixes"
	"github.com/BurntSushi/xgb/xproto"
)

// TODO: image support - image blob stores in file if large? same for large text clips?

// TODO: serialise to disk + resume on restart https://pkg.go.dev/encoding/gob
// on each copy? cheaper and nicer to append to a text file and do ocassional vaccum?
// encode newlines then use newline as a separator? -- https://pkg.go.dev/encoding/base64@go1.19.1
// fuzztest that does some roundtrips
// vaccum = copy the last 50 lines to a new file and then unlink the old file I guess

// If we _are_ only appending, then duplicates will be appended too. Either we leave far more in the file so that we
// can drop them when we vaccum, or we only persist the _previous_ clip? (or we wait for 15s before we append), so that
// we know that we won't need to replace the previous line in the file.

// Oooorr.. we just persist the whole damn thing every now and then (15s of no activity?). This is probably good enough, right? This isn't
// critical.

// I mean really is this important at all? Probably not a priority actually.

// TODO: show the source window name (configurable?)

// TODO: better readme + automatic builds + aur

var X *xgb.Conn
var screen *xproto.ScreenInfo
var selectionPropertyAtom, clipboardAtom xproto.Atom

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
	var sock string
	var historySize int

	flag.Usage = usage
	flag.StringVar(&sock, "socket", "/tmp/clipclop.sock", "location of the socket file")
	flag.IntVar(&historySize, "n", 100, "Number of records to keep in history")
	flag.Parse()

	logger = log.New(os.Stdout, "", log.Lshortfile|log.Ldate|log.Ltime)
	history = newHistory(historySize)

	logger.Print("Connecting to X")
	startX()

	logger.Print("Creating event window")
	createEventWindow()

	go ipcServer(sock)
	processEvents()
}

func processEvents() {
	for {
		ev, xerr := X.WaitForEvent()
		if ev == nil && xerr == nil {
			logger.Fatal("Wait for event failed")
			return
		}

		if ev != nil {
			switch ev.(type) {
			case xfixes.SelectionNotifyEvent:
				// A selection has been made, request it
				err := requestSelection(ev.(xfixes.SelectionNotifyEvent))
				if err != nil {
					logger.Fatalf("Error requesting selection convert %s", err)
					return
				}
			case xproto.SelectionNotifyEvent:
				// The selection is ready to be read
				s, err := extractSelection(ev.(xproto.SelectionNotifyEvent))
				if err != nil {
					logger.Fatalf("Failed to get selection prop %s", err)
				} else if len(s) > 0 {
					history.Append(Clip{time.Now(), s, "unknown"})
				}
			default:
				logger.Printf("Unknown Event: %s\n", ev)
			}
			// TODO: Not sure about TARGETS, try getting straight text first. -- for image will need to check TARGETS for
			// presence of img/png type
			// TODO: if fmtid is INCRID then we need extra logic for that
		}
		if xerr != nil {
			logger.Printf("Error waiting for event: %s\n", xerr)
		}
	}
}

func startX() {
	conn, err := xgb.NewConn()
	if err != nil {
		logger.Fatal("Could not create X connection ", err)
	}
	X = conn

	if err = xfixes.Init(X); err != nil {
		logger.Fatal("Could not init xfixes extension ", err)
	}

	setup := xproto.Setup(X)
	screen = setup.DefaultScreen(X)

	// From https://www.x.org/releases/current/doc/fixesproto/fixesproto.txt
	// The client must negotiate the version of the extension before executing
	// extension requests.  Behavior of the server is undefined otherwise.
	if _, err = xfixes.QueryVersion(X, 2, 0).Reply(); err != nil {
		logger.Fatal("Could not negotiate xfixes version ", err)
	}

	selectionPropertyAtom = createAtom("CLIPCLOP_SEL")
	clipboardAtom = createAtom("CLIPBOARD")
}

func createEventWindow() {
	wid, err := xproto.NewWindowId(X)
	if err != nil {
		logger.Fatal("Could not get window ID ", err)
	}

	err = xproto.CreateWindowChecked(
		X, screen.RootDepth, wid, screen.Root, 0, 0, 1, 1, 0,
		xproto.WindowClassInputOutput, screen.RootVisual,
		0, []uint32{},
	).Check()
	if err != nil {
		logger.Fatal("Could not create event window ", err)
	}

	// Show the window -- we don't need to do this to get events, so by leaving it unmapped it is hidden from view!
	// xproto.MapWindowChecked(X, wid).Check()

	// Request events to it when the selection changes
	var mask uint32 = xfixes.SelectionEventMaskSetSelectionOwner
	if err = xfixes.SelectSelectionInputChecked(X, wid, xproto.AtomPrimary, mask).Check(); err != nil {
		logger.Fatal("Could not select primary selection events ", err)
	}

	if err = xfixes.SelectSelectionInputChecked(X, wid, clipboardAtom, mask).Check(); err != nil {
		logger.Fatal("Could not select clopboard selection events ", err)
	}
}

func requestSelection(ev xfixes.SelectionNotifyEvent) error {
	return xproto.ConvertSelectionChecked(X, ev.Window, ev.Selection, xproto.AtomString, selectionPropertyAtom, ev.SelectionTimestamp).Check()
}

func extractSelection(ev xproto.SelectionNotifyEvent) (string, error) {
	reply, err := xproto.GetProperty(X, true, ev.Requestor, selectionPropertyAtom, xproto.AtomString, 0, (1<<32)-1).Reply()
	if err != nil {
		return "", err
	}
	return string(reply.Value), nil
}

func createAtom(n string) xproto.Atom {
	reply, err := xproto.InternAtom(X, false, uint16(len(n)), n).Reply()
	if err != nil {
		logger.Fatalf("Could not create atom %s", n)
	}
	return reply.Atom
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
		return clip.value
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
