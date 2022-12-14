package history

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

type ClipFormat int

const lineLen = 60

const (
	NoneFormat ClipFormat = iota
	StringFormat
	PngFormat
)

type Clip struct {
	Created time.Time
	Value   []uint8
	Format  ClipFormat
	Source  string
}

type History struct {
	data     []Clip
	presets  []Clip
	first    int
	selected *Clip
	mu       sync.RWMutex
}

func NewHistory(maxSize int, presets []string) *History {
	presetClips := make([]Clip, 0, len(presets))
	for _, s := range presets {
		presetClips = append(presetClips, Clip{
			Value:  []uint8(s),
			Format: StringFormat,
			Source: "preset",
		})
	}

	h := History{
		data:    make([]Clip, 0, maxSize),
		presets: presetClips,
		first:   0,
	}
	return &h
}

func (h *History) SetSelected(c *Clip) {
	h.selected = c
}

func (h *History) GetSelected() *Clip {
	if h.selected == nil {
		return h.Top()
	}
	return h.selected
}

func (h *History) Top() *Clip {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.data) < 1 {
		if len(h.presets) > 0 {
			return &h.presets[0]
		}
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

	cnt := len(h.data) + len(h.presets)
	r := make([]string, 0, cnt)

	if len(h.data) > 0 {
		// iterate backwards to show the more recent entries first
		// TODO: could we extract this looping?
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
	}

	// Include the presets at the end
	for _, p := range h.presets {
		r = append(r, f(p))
	}

	return r
}

func (h *History) FindEntry(formatted string) (*Clip, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.data)+len(h.presets) == 0 {
		return nil, errors.New("empty history")
	}

	search, err := removeRelativeTimeString(formatted)
	if err != nil {
		return nil, err
	}
	search = strings.Trim(search, "\n ")
	i := h.getEnd()

	isMatch := func(c Clip) bool {
		s := HistoryFormatter(c)
		s, _ = removeRelativeTimeString(s)
		return strings.Trim(s, "\n ") == search
	}

	if len(h.data) > 0 {
		for {
			if isMatch(h.data[i]) {
				return &h.data[i], nil
			}
			if i == h.first {
				break // we've gone full circle
			}
			if i--; i < 0 {
				i = len(h.data) - 1
			}
		}
	}

	for _, p := range h.presets {
		if isMatch(p) {
			return &p, nil
		}
	}

	return nil, errors.New("no match found")
}

func HistoryFormatter(c Clip) string {
	var line, post string
	pre := fmt.Sprintf("[%s] ", getRelativeTimeString(c.Created))

	if c.Format == PngFormat {
		line = fmt.Sprintf("{png image %.1fkB}", float32(len(c.Value))/1024.0)
	} else {
		lines := strings.Split(string(c.Value), "\n")
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

// undefined if empty
func (h *History) getEnd() int {
	lastIndex := h.first - 1
	if lastIndex < 0 {
		return len(h.data) - 1
	}
	return lastIndex
}

func (c *Clip) isDuplicate(c2 Clip) bool {
	if c2.Created.Sub(c.Created).Seconds() > 15 {
		// if 15s have passed, we assume this is not a duplicate
		return false
	}
	return strings.Contains(string(c.Value), string(c2.Value)) || strings.Contains(string(c2.Value), string(c.Value))
}

func getRelativeTimeString(t time.Time) string {
	if t.IsZero() {
		return " preset"
	}

	td := time.Since(t)
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
		return "", errors.New("missing relative time from selection")
	}

	return s[start+1:], nil
}
