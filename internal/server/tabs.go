package server

import (
	"path/filepath"
	"strings"
	"time"
)

// maxTabs caps the strip so a long browsing session does not grow it forever.
const maxTabs = 20

// maxStreams caps how many event streams are held open at once. Browsers allow
// six connections per host over HTTP/1.1 and a stream occupies one for as long
// as its page is open, so unbounded viewers eventually starve ordinary page
// loads: the seventh document just hangs. Beyond this many, the least recently
// used viewer is asked to yield its connection.
const maxStreams = 4

// titleSuffix is appended to every page title. `mdlocalhost open` looks for it
// in window titles to bring an already-open viewer to the front, so the page
// templates must keep using it.
const titleSuffix = " · " + AppName

// tabEntry is one document open in the viewer.
type tabEntry struct {
	Path string
	Name string
	Seen time.Time
}

// tabView is the shape rendered into the strip and returned by /tabs.json.
type tabView struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

// busMsg is a server-sent event pushed to open viewers.
type busMsg struct {
	Event string
	Data  string
}

// viewer is one open page holding an event stream. Messages are dropped rather
// than blocking when a stream falls behind.
type viewer struct {
	id      string
	ch      chan busMsg
	opened  time.Time
	focused time.Time
}

// active is when this viewer was last known to be in front of the user.
func (v *viewer) active() time.Time {
	if v.focused.After(v.opened) {
		return v.focused
	}
	return v.opened
}

// addTab records a document as open, moving an existing entry to the front of
// the recency order without disturbing its position in the strip.
func (s *srv) addTab(abs string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.tabs {
		if strings.EqualFold(s.tabs[i].Path, abs) {
			s.tabs[i].Seen = time.Now()
			return
		}
	}
	s.tabs = append(s.tabs, tabEntry{Path: abs, Name: filepath.Base(abs), Seen: time.Now()})
	if len(s.tabs) > maxTabs {
		s.tabs = s.tabs[len(s.tabs)-maxTabs:]
	}
}

// closeTab removes a document from the strip and returns the URL of the tab
// that should take its place, or "/" when it was the last one.
func (s *srv) closeTab(abs string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i := range s.tabs {
		if strings.EqualFold(s.tabs[i].Path, abs) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ""
	}
	s.tabs = append(s.tabs[:idx], s.tabs[idx+1:]...)
	if len(s.tabs) == 0 {
		return "/"
	}
	if idx >= len(s.tabs) {
		idx = len(s.tabs) - 1
	}
	return viewPath(s.tabs[idx].Path)
}

func (s *srv) tabViews(active string) []tabView {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]tabView, 0, len(s.tabs))
	for _, t := range s.tabs {
		out = append(out, tabView{
			Name:   t.Name,
			Path:   t.Path,
			URL:    viewPath(t.Path),
			Active: active != "" && strings.EqualFold(t.Path, active),
		})
	}
	return out
}

// addViewer registers an event stream. When too many are open the least
// recently used one is told to yield, which keeps a browser's per-host
// connection pool from being exhausted by idle viewers.
func (s *srv) addViewer(id string) *viewer {
	v := &viewer{id: id, ch: make(chan busMsg, 8), opened: time.Now()}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.viewers[v] = struct{}{}
	for len(s.viewers) > maxStreams {
		var lru *viewer
		for other := range s.viewers {
			if other == v {
				continue
			}
			// A page left over from an older build cannot be handed documents
			// anyway, so it yields its connection before a current one does.
			if lru != nil && (other.id == "") != (lru.id == "") {
				if other.id == "" {
					lru = other
				}
				continue
			}
			if lru == nil || other.active().Before(lru.active()) {
				lru = other
			}
		}
		if lru == nil {
			break
		}
		delete(s.viewers, lru)
		select {
		case lru.ch <- busMsg{Event: "busy"}:
		default:
		}
	}
	return v
}

func (s *srv) dropViewer(v *viewer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.viewers, v)
}

// noteFocus records that a viewer's page came to the front, which decides who
// receives the next document opened from Explorer.
func (s *srv) noteFocus(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for v := range s.viewers {
		if v.id == id {
			v.focused = time.Now()
		}
	}
}

func (s *srv) broadcast(m busMsg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for v := range s.viewers {
		select {
		case v.ch <- m:
		default:
		}
	}
}

// dispatchOpen hands a document to the viewer the user used most recently and
// reports whether one took it. Nobody home means the caller opens a browser.
func (s *srv) dispatchOpen(u string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	var best *viewer
	for v := range s.viewers {
		// Pages served by an older build do not know how to take a document;
		// they connect without identifying themselves, and are skipped here so
		// the file opens in a new window rather than vanishing.
		if v.id == "" {
			continue
		}
		if best == nil || v.active().After(best.active()) {
			best = v
		}
	}
	if best == nil {
		return false
	}
	select {
	case best.ch <- busMsg{Event: "open-doc", Data: u}:
		return true
	default:
		return false
	}
}
