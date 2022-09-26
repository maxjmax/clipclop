package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Clip struct {
	created time.Time
	value   string
	source  string
}

type History struct {
	data  []Clip
	first int
	mu    sync.RWMutex
}

func newHistory(maxSize int) *History {
	h := History{
		data:  make([]Clip, 0, maxSize),
		first: 0,
	}
	return &h
}

// undefined if empty
func (h *History) getEnd() int {
	lastIndex := h.first - 1
	if lastIndex < 0 {
		return len(h.data) - 1
	}
	return lastIndex
}

func (c *Clip) isDuplicate(c2 Clip) bool {
	if c2.created.Sub(c.created).Seconds() > 60*5 {
		// if 5 minutes have passed, we assume this is not a duplicate
		return false
	}
	return strings.Contains(c.value, c2.value) || strings.Contains(c2.value, c.value)
}

func (h *History) Append(c Clip) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.data) > 0 {
		end := h.getEnd()
		if h.data[end].isDuplicate(c) {
			// replace the end rather than adding a new record
			h.data[end] = c
			return
		}
	}
	if len(h.data) < cap(h.data) {
		// first time through, fill up the buffer
		h.data = append(h.data, c)
	} else {
		h.data[h.first] = c
		// if we reach the end, we loop back around
		h.first = (h.first + 1) % cap(h.data)
	}
}

func (h *History) Format(f func(Clip) string) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.data) == 0 {
		return []string{}
	}

	i := h.first
	r := make([]string, 0, len(h.data))

	for {
		r = append(r, f(h.data[i]))
		i = (i + 1) % len(h.data)
		if i == h.first {
			break // we've gone full circle
		}
	}

	return r
}

func getRelativeTimeString(td time.Duration) string {
	s := td.Seconds()
	if s < 120 {
		return fmt.Sprintf("%2ds ago", int(s))
	}
	if s < 120*60 {
		return fmt.Sprintf("%2dm ago", int(s/60))
	}
	return fmt.Sprintf("%2dh ago", int(s/60*60))
}

func removeRelativeTimeString(s string) (string, error) {
	start := strings.Index(s, "]")
	if start < 0 {
		return "", errors.New("Missing relative time from selection")
	}

	return s[start+1:], nil
}

func (h *History) FindEntry(formatted string) (*Clip, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.data) == 0 {
		return nil, errors.New("Empty history")
	}

	search, err := removeRelativeTimeString(strings.Trim(formatted, "\n "))
	if err != nil {
		return nil, err
	}

	i := h.first
	for {
		s := HistoryFormatter(h.data[i])
		s, _ = removeRelativeTimeString(s)
		if s == search {
			return &h.data[i], nil
		}
		i = (i + 1) % len(h.data)
		if i == h.first {
			break // we've gone full circle
		}
	}
	return nil, errors.New("No match found")
}

func HistoryFormatter(c Clip) string {
	t := getRelativeTimeString(time.Now().Sub(c.created))

	lines := strings.Split(c.value, "\n")
	line := lines[0]
	if len(line) > 50 {
		// TODO: not unicode safe
		line = line[:47] + "..."
	}
	if len(lines) > 1 {
		line = fmt.Sprintf("%s [+%d lines]", line, len(lines)-1)
	}
	return fmt.Sprintf("[%s] %s", t, line)
}
