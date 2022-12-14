//go:build integration
// +build integration

package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"
)

var opts = options{
	Sock:        "/tmp/testing-sock.sock",
	MinClipSize: 4,
	Debug:       false,
	HistorySize: 50,
}

func TestClipClopIntegration(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	opts := opts
	opts.Presets = []string{"preset string"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // cleanup when test is done

	go run(ctx, logger, opts)

	// TODO: better way
	time.Sleep(500 * time.Millisecond) // let it start up

	// Ensure we can SEL if there is only a preset
	out := checkGET(t, 1)
	checkSEL(t, out[0], "preset string")

	// in another thread, change the clipboard using xclip
	clips := [][]string{
		{"clipboard", "bla"},  // too short, will be discarded
		{"clipboard", "blaa"}, // just long enough, will be included
		{"clipboard", "hello world"},
		{"clipboard", "wee %*21"},
		{"primary", "we ignore primary selections, now"},
		{"clipboard", "awkrwere\nwrir rwerr jwer "},
	}

	expected := strings.Join(
		[]string{
			"[ 1s ago] awkrwere                                [+1 lines]",
			"[ 1s ago] wee %*21                                          ",
			"[ 1s ago] hello world                                       ",
			"[ 1s ago] blaa                                              ",
			"[ preset] preset string                                     ",
		}, "\n")

	populateClips(clips)

	out = checkGET(t, 5)
	if strings.Join(out, "\n") != expected {
		t.Fatalf("got %s\nexpected %s", out, expected)
	}

	rand.Seed(time.Now().UnixNano())

	randomClips := make([][]string, 0, 100)
	for i := 0; i < 100; i++ {
		str := randString(50 + rand.Intn(100))
		randomClips = append(randomClips, []string{"clipboard", str})
	}

	populateClips(randomClips)
	lines := checkGET(t, 51) // 50 + 1 preset
	for i := 0; i < 50; i++ {
		clip := randomClips[100-(i+1)]

		// if we select the first line, it should return the 50th clip (since they are in reverse order)
		checkSEL(t, lines[i], clip[1])
	}
}

func TestClipClopClipboardOwnership(t *testing.T) {
	logger := log.New(io.Discard, "", 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // cleanup when test is done

	go run(ctx, logger, opts)

	clips := [][]string{
		{"clipboard", "hello world"},
		{"clipboard", "another world"},
	}

	populateClips(clips)

	// Select the second clip
	lines := checkGET(t, 2)
	checkSEL(t, lines[1], "hello world")

	// Now do another copy, and immediately paste
	clips = [][]string{
		{"clipboard", "third world"},
	}
	populateClips(clips)

	fullClip, _ := getSelWithXclip()
	if fullClip != "third world" {
		t.Fatalf("Should have pasted the last copied entry, but got %s", fullClip)
	}
}

func TestClipClopINCR(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // cleanup when test is done

	go run(ctx, logger, opts)

	// TODO: ??? seems to work in browser with a big image (10MB)
	// TODO: problem with our xclip call?
	// TODO: we're only getting 1MB back, so assume we're not writing the whole 2MB
	// TODO: any bigger than 100 and it starts to use INCR and it stops working, but it _does_ work
	// aaah
	clips := [][]string{{"clipboard", strings.Repeat("1234567890", 100*1024)}}
	populateClips(clips)

	lines := checkGET(t, 1)
	checkSEL(t, lines[0], clips[0][1])
}

func checkGET(t *testing.T, expectedCnt int) []string {
	out, err := sendCommandToSocket("GET\n")
	if err != nil {
		t.Fatal("Could not talk with clipclop", err)
	}

	lines := strings.Split(out, "\n")
	if len(lines) != expectedCnt {
		t.Fatalf("Wrong number of entries found: expected %d, got %d", expectedCnt, len(lines))
	}
	return lines
}

func checkSEL(t *testing.T, line string, expected string) {
	out, err := sendCommandToSocket(fmt.Sprintf("SEL %s\n", line))
	if err != nil || out != "OK" {
		t.Fatalf("Could not set clip %s: %s, err: %s", line, out, err)
	}

	fullClip, err := getSelWithXclip()
	if err != nil {
		t.Fatalf("Could not get selection after SEL %s", line)
	}
	if fullClip != expected {
		if len(fullClip) > 512 {
			t.Fatalf("Got %dkB instead of expected %dkB when selecting %s", len(fullClip)/1024, len(expected)/1024, line)
		}
		t.Fatalf("Tried to sel %s using %s but got %s", expected, line, fullClip)
	}
}

var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ\n~012345^\t????????????     ")

func randString(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func populateClips(clips [][]string) {
	for _, clip := range clips {
		err := setSelWithXclip(clip[1], clip[0])
		if err != nil {
			panic(err)
		}
	}

	time.Sleep(1 * time.Second) // TODO: eeeeh..find a better way. need to wait for the xevents to trickle through
}

func sendCommandToSocket(cmd string) (string, error) {
	// then try and SEL using the socket
	conn, err := net.Dial("unix", "/tmp/testing-sock.sock")
	if err != nil {
		return "", fmt.Errorf("could not connect to socket: %w", err)
	}
	n, err := conn.Write([]byte(cmd))
	if n < 1 || err != nil {
		return "", fmt.Errorf("could not write to socket: %w", err)
	}
	out, err := io.ReadAll(conn)
	if err != nil {
		return "", fmt.Errorf("could not read from socket: %w", err)
	}
	return strings.Trim(string(out), "\n"), nil
}

func setSelWithXclip(val string, sel string) error {
	// Seems to struggle over 1MB?
	// But we won't use INCR until ~1.7MB?
	// Which makes the test useless.
	// Not certain it's xclip's fault yet
	// https://github.com/astrand/xclip/commit/4601354aff7d4bb62c3340c38ea66d61020be913 ???
	cmd := exec.Command("xclip", "-i", "-selection", sel)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	go func() {
		defer stdin.Close()
		io.WriteString(stdin, val)
	}()

	return cmd.Run()
}

func getSelWithXclip() (string, error) {
	cmd := exec.Command("xclip", "-o", "-selection", "primary")

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}

	return string(out), nil
}
