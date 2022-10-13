package history

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

func newTestClip(s string) Clip {
	return Clip{time.Now(), []uint8(s), StringFormat, "test"}
}

func getHistoryAsLines(h *History, sep string) string {
	return strings.Join(h.Format(func(c Clip) string { return string(c.Value) }), sep)
}

func TestHistory(t *testing.T) {
	h := NewHistory(6)
	expected := []string{
		"",
		"0",
		"1 0",
		"2 1 0",
		"3 2 1 0",
		"4 3 2 1 0",
		"5 4 3 2 1 0",
		"6 5 4 3 2 1",
		"7 6 5 4 3 2",
		"8 7 6 5 4 3",
		"9 8 7 6 4 3",
	}

	for i := 0; i < 9; i++ {
		formatted := getHistoryAsLines(h, " ")
		if expected[i] != formatted {
			t.Errorf("History was wrong: got %s expected %s", formatted, expected[i])
		}

		h.Append(newTestClip(fmt.Sprint(i)))
	}
}

func TestDuplicates(t *testing.T) {
	h := NewHistory(6)

	h.Append(newTestClip("Hello"))       // dup
	h.Append(newTestClip("Hell"))        // dup
	h.Append(newTestClip("Hello world")) // dup
	h.Append(newTestClip("Helo world"))  // not a dup

	got := getHistoryAsLines(h, "|")
	if got != "Helo world|Hello world" {
		t.Errorf("History was wrong, got %s", got)
	}
}

func TestHistoryFormat(t *testing.T) {
	h := NewHistory(1)

	clips := []string{
		"Hello",
		" 	Hello",
		strings.Repeat("Hello", 12),
		"Hello\nHello\nHello",
		strings.Repeat("Hello", 12) + "\nHello",
	}

	expected := []string{
		"[ 0s ago] {png image 0.0kB}                                 ",
		"[ 0s ago] HelloHelloHelloHelloHelloHelloHelloH... [+1 lines]",
		"[ 0s ago] Hello                                   [+2 lines]",
		"[ 0s ago] HelloHelloHelloHelloHelloHelloHelloHelloHelloHe...",
		"[ 0s ago] Hello                                             ",
		"[ 0s ago] Hello                                             ",
	}

	for _, s := range clips {
		h.Append(newTestClip(s))
	}
	h.Append(Clip{time.Now(), []uint8{}, PngFormat, "test"})

	f := h.Format(HistoryFormatter)

	for i := range f {
		if f[i] != expected[i] {
			t.Errorf("Format was wrong, expected %s got %s", expected[i], f[0])
		}
	}
}

func TestHistorySelect(t *testing.T) {
	e := []string{
		"This is a clip with some text\nand multiple lines. It is probably quite long.",
		strings.Repeat("Hello world", 10),
		"A\nB\nC\nD\nE\n",
		"ABCD",
		"This is a clip",
		"clip",
		"c",
		"%@&",
	}

	for i := 0; i < 5; i++ {
		// Shuffle the array differently each time to make sure order doesn't matter
		rand.Shuffle(len(e), func(i, j int) { e[i], e[j] = e[j], e[i] })
		clips := make([]Clip, 0, len(e))
		h := NewHistory(10)
		for i, str := range e {
			// separate the clip times to avoid removal of dups
			clip := Clip{time.Now().Add(time.Hour * time.Duration(i)), []uint8(str), StringFormat, "test"}
			clips = append(clips, clip)
			h.Append(clip)
		}

		formatted := h.Format(HistoryFormatter)
		for j := 0; j < len(e); j++ {
			realIndex := len(e) - (j + 1) // because we get the first item last
			// try to find each entry
			c, err := h.FindEntry(formatted[realIndex])
			if err != nil {
				t.Fatalf("Error finding entry %s: %s", formatted[realIndex], err)
			} else if string(c.Value) != string(clips[j].Value) {
				t.Fatalf("Wrong entry found for %s:\n%v !=\n%v", formatted[realIndex], c, clips[realIndex])
			}
		}
	}
}

func TestHistoryTimeString(t *testing.T) {
	durations :=
		[]struct {
			in  string
			out string
		}{
			{"1s", " 1s ago"},
			{"60s", "60s ago"},
			{"80s", "80s ago"},
			{"140s", " 2m ago"},
			{"3m", " 3m ago"},
			{"60m", "60m ago"},
			{"180m", " 3h ago"},
			{"12h", "12h ago"},
			{"17h20m", "17h ago"},
			{"17h40m", "18h ago"},
			{"25h", "25h ago"},
			{"120h", " 5d ago"},
		}

	for _, tt := range durations {
		d, err := time.ParseDuration(tt.in)
		if err != nil {
			t.Fatalf("could not parse duration: %s", err)
		}
		r := getRelativeTimeString(d)
		if r != tt.out {
			t.Fatalf("Incorrect duration string: got %s expected %s", r, tt.out)
		}
	}
}
