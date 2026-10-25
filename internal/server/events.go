package server

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// handleEvents is the one stream a viewer keeps open. It carries live reload
// for the document being viewed (when ?p= names one) and the messages that let
// a file double-clicked in Explorer land in this page instead of a new browser
// tab.
func (s *srv) handleEvents(w http.ResponseWriter, r *http.Request) {
	var target string
	if raw := r.URL.Query().Get("p"); raw != "" {
		t, err := urlPathToPath(raw)
		if err != nil {
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		target = filepath.Clean(t)
		if !s.authorized(target) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	var watchEvents <-chan fsnotify.Event
	var watchErrors <-chan error
	if target != "" {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			http.Error(w, "cannot watch file: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer watcher.Close()

		// Watch the containing folder: editors routinely save by writing a
		// temporary file and renaming it over the original, which destroys a
		// watch registered on the file itself.
		if err := watcher.Add(filepath.Dir(target)); err != nil {
			http.Error(w, "cannot watch folder: "+err.Error(), http.StatusInternalServerError)
			return
		}
		watchEvents, watchErrors = watcher.Events, watcher.Errors
	}

	v := s.addViewer(r.URL.Query().Get("v"))
	defer s.dropViewer(v)

	s.sseCount.Add(1)
	defer s.sseCount.Add(-1)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 2000\n\n")
	flusher.Flush()

	base := strings.ToLower(filepath.Base(target))
	lastSent := time.Now()
	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	defer debounce.Stop()

	keepAlive := time.NewTicker(25 * time.Second)
	defer keepAlive.Stop()

	send := func(event, data string) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		flusher.Flush()
		lastSent = time.Now()
	}

	for {
		select {
		case <-r.Context().Done():
			return

		case m := <-v.ch:
			if m.Event == "busy" {
				// Too many viewers are holding connections open. Yield this
				// one and come back later rather than starving page loads.
				fmt.Fprint(w, "retry: 30000\n\n")
				send("busy", "1")
				return
			}
			send(m.Event, m.Data)

		case ev, ok := <-watchEvents:
			if !ok {
				return
			}
			if !strings.EqualFold(filepath.Base(ev.Name), base) {
				continue
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
				continue
			}
			// Coalesce the burst of events a single save produces.
			debounce.Reset(150 * time.Millisecond)

		case <-debounce.C:
			if _, err := os.Stat(target); err != nil {
				send("gone", "1")
				continue
			}
			if time.Since(lastSent) < 100*time.Millisecond {
				continue
			}
			send("reload", fmt.Sprint(time.Now().UnixMilli()))

		case err, ok := <-watchErrors:
			if !ok {
				return
			}
			log.Printf("watch error: %v", err)

		case <-keepAlive.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
