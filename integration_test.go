//go:build integration
// +build integration

package main

import (
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
	go run(logger, opts)

	// in another thread, change the clipboard using xclip
	clips := [][]string{
		{"clipboard", "bla"},  // too short, will be discarded
		{"clipboard", "blaa"}, // just long enough, will be included
		{"primary", "hello world"},
		{"clipboard", "wee %*21"},
		{"primary", "awkrwere\nwrir rwerr jwer "},
	}

	expected := strings.Join(
		[]string{
			"[ 1s ago] awkrwere                                [+1 lines]",
			"[ 1s ago] wee %*21                                          ",
			"[ 1s ago] hello world                                       ",
			"[ 1s ago] blaa                                              ",
		}, "\n")

	err := populateClips(clips)
	if err != nil {
		t.Fatalf("could not populate clipboard history: %s", err)
	}

	out, err := sendCommandToSocket("GET\n")
	if err != nil {
		t.Fatal("Could not talk with clipclop", err)
	}

	if out != expected {
		t.Fatalf("got %s\nexpected %s", out, expected)
	}

	rand.Seed(time.Now().UnixNano())

	randomClips := make([][]string, 0, 100)
	for i := 0; i < 100; i++ {
		str := randString(50 + rand.Intn(100))
		randomClips = append(randomClips, []string{"primary", str})
	}

	populateClips(randomClips)

	out, err = sendCommandToSocket("GET\n")
	if err != nil {
		t.Fatal("Could not talk with clipclop", err)
	}

	lines := strings.Split(out, "\n")
	if len(lines) != 50 {
		t.Fatalf("Should have 50 entries but got %d", len(lines))
	}

	for i := 0; i < 50; i++ {
		clip := randomClips[100-(i+1)]
		line := lines[i]

		// if we select the first line, it should return the 50th clip (since they are in reverse order)
		out, err = sendCommandToSocket(fmt.Sprintf("SEL %s\n", line))
		if err != nil || out != "OK" {
			t.Fatalf("Could not set clip %s: %s, err: %s", line, out, err)
		}

		fullClip, err := getSelWithXclip()
		if err != nil {
			t.Fatalf("Could not get selection")
		}
		if fullClip != clip[1] {
			t.Fatalf("Tried to sel %s using %s but got %s", clip[1], line, fullClip)
		}
	}
}

func TestClipClopINCR(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	go run(logger, opts)

	clips := [][]string{{"primary", strings.Repeat("1234567890", 8*1024)}}
	err := populateClips(clips)
	if err != nil {
		t.Fatalf("could not populate clipboard history: %s", err)
	}

	out, err := sendCommandToSocket("GET\n")
	lines := strings.Split(out, "\n")
	if len(lines) != 1 {
		t.Fatalf("Should have 1 entry but got %d", len(lines))
	}

	// TODO: SOMETIMES fails, presumably when it tries to use INCR to set the property, which we don't support.
	// TODO: setting to 200kB seems to cause it to always fail, indeed.
	out, err = sendCommandToSocket(fmt.Sprintf("SEL %s\n", lines[0]))
	if err != nil || out != "OK" {
		t.Fatalf("Could not set clip: %s, %s", out, err)
	}

	fullClip, err := getSelWithXclip()
	if err != nil {
		t.Fatalf("Could not get selection")
	}
	if fullClip != clips[0][1] {
		// Max prop size is probably set to ~63kB
		// (we chunk it above 1/4 of the max size, which would be that many int32s)
		t.Fatalf("Did not get full 80kB clip back, only got %dkB", len(fullClip)/1024)
	}
}

var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ\n~012345^\t更多É😊     ")

func randString(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func populateClips(clips [][]string) error {
	for _, clip := range clips {
		err := setSelWithXclip(clip[1], clip[0])
		if err != nil {
			return err
		}
	}

	time.Sleep(500 * time.Millisecond) // TODO: eeeeh..find a better way. need to wait for the xevents to trickle through
	return nil
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
