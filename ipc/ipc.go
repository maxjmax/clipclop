package ipc

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"github.com/maxjmax/clipclop/history"
	"github.com/maxjmax/clipclop/x"
)

func IPCServer(ctx context.Context, logger *log.Logger, hist *history.History, xconn *x.X, sock string) {
	if err := os.RemoveAll(sock); err != nil {
		logger.Fatalf("could not remove IPC socket file %s", sock)
	}

	listener, err := net.Listen("unix", sock)
	if err != nil {
		logger.Fatalf("could not listen on %s: %s", sock, err)
	}
	defer listener.Close()
	logger.Printf("Listening on socket %s", sock)

	go func() {
		// TODO: Not 100% sure this is the best way of doing this. Same in main's event loop.
		<-ctx.Done()
		logger.Print("Shutting down")
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			logger.Print("could not accept connection ", err)
			return
		}

		err = handleConnection(conn, hist, xconn)
		if err != nil {
			logger.Printf("error handling connection: %s", err)
		}
	}
}

func handleConnection(conn net.Conn, hist *history.History, xconn *x.X) error {
	defer conn.Close()
	buff := make([]byte, 256)
	_, err := conn.Read(buff)
	if err != nil {
		return fmt.Errorf("could not read from connection: %w", err)
	}
	nl := strings.IndexRune(string(buff), '\n')
	if nl < 0 {
		return fmt.Errorf("did not find newline in command: %w", err)
	}
	output := handleCommand(string(buff)[:nl], hist, xconn)
	_, err = conn.Write([]byte(output))
	if err != nil {
		return fmt.Errorf("could not write output: %w", err)
	}

	return nil
}

func handleCommand(cmd string, hist *history.History, xconn *x.X) string {
	// TODO: don't like passing the history dowm, needs refactoring
	// TODO: we could wrap it in an IPCServer object, not convinced that's _better_ though.
	if len(cmd) < 3 {
		return "ERR Invalid command" // commands are 3 characters
	}
	switch cmd[:3] {
	case "GET":
		return strings.Join(hist.Format(history.HistoryFormatter), "\n") + "\n"
	case "SEL":
		clip, err := hist.FindEntry(cmd[3:])
		if err != nil {
			return fmt.Sprintf("ERR Not found: %s", err)
		}

		hist.SetSelected(clip)
		err = xconn.BecomeSelectionOwner()
		if err != nil {
			return fmt.Sprintf("ERR Could not become owner: %s", err)
		}
		return "OK"
	default:
		return "ERR Unknown command"
	}
}
