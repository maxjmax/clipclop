package main

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

type Format int

const lineLen = 60

const (
	NoneFormat Format = iota
	StringFormat
	PngFormat
)

type Clip struct {
	created time.Time
	value   []uint8
	format  Format
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
	if c2.created.Sub(c.created).Seconds() > 15 {
		// if 15s have passed, we assume this is not a duplicate
		return false
	}
	return strings.Contains(string(c.value), string(c2.value)) || strings.Contains(string(c2.value), string(c.value))
}

func (h *History) Top() *Clip {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.data) < 1 {
		return nil
	}
	return &h.data[h.getEnd()]
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

	r := make([]string, 0, len(h.data))

	// iterate backwards to show the more recent entries first
	i := h.getEnd()
	for {
		r = append(r, f(h.data[i]))
		if i == h.first {
			break // we've gone full circle
		}
		if i--; i < 0 {
			i = len(h.data) - 1
		}
	}

	return r
}

func getRelativeTimeString(td time.Duration) string {
	s := td.Seconds()
	if s < 120 {
		return fmt.Sprintf("%2ds ago", int(math.Round(s)))
	}
	if s < 120*60 {
		return fmt.Sprintf("%2dm ago", int(math.Round(s/60)))
	}
	if s < 120*60*24 {
		return fmt.Sprintf("%2dh ago", int(math.Round(s/(60*60))))
	}
	return fmt.Sprintf("%2dd ago", int(math.Round(s/(60*60*24))))
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

	search, err := removeRelativeTimeString(formatted)
	if err != nil {
		return nil, err
	}
	search = strings.Trim(search, "\n ")

	i := h.getEnd()
	for {
		s := HistoryFormatter(h.data[i])
		s, _ = removeRelativeTimeString(s)
		if strings.Trim(s, "\n ") == search {
			return &h.data[i], nil
		}
		if i == h.first {
			break // we've gone full circle
		}
		if i--; i < 0 {
			i = len(h.data) - 1
		}
	}
	return nil, errors.New("No match found")
}

func HistoryFormatter(c Clip) string {
	var line, post string
	pre := fmt.Sprintf("[%s] ", getRelativeTimeString(time.Now().Sub(c.created)))

	if c.format == PngFormat {
		line = fmt.Sprintf("{png image %.1fkB}", float32(len(c.value))/1024.0)
	} else {
		lines := strings.Split(string(c.value), "\n")
		line = strings.Trim(lines[0], " \n\t")
		if len(lines) > 1 {
			post = fmt.Sprintf(" [+%d lines]", len(lines)-1)
		}
	}

	rem := lineLen - len(pre) - len(post)
	if len(line) > rem {
		// TODO: not unicode safe
		line = line[:(rem-3)] + "..."
	}
	return fmt.Sprintf("%s%*s%s", pre, -rem, line, post)
}
