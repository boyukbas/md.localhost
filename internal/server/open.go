package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Open makes sure a server is running and shows the file. A viewer that is
// already open takes it as a new tab of its own; otherwise a window is opened.
func Open(abs string, hostOverride string, mode WindowMode, reuse bool) error {
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("cannot open %s: %w", abs, err)
	}
	abs = filepath.Clean(abs)

	st, err := ensureServer()
	if err != nil {
		return err
	}

	res, err := requestOpen(st, abs, reuse)
	if err != nil {
		// A server that was alive a moment ago may have just gone idle.
		clearState()
		if st, err = ensureServer(); err != nil {
			return err
		}
		if res, err = requestOpen(st, abs, reuse); err != nil {
			return err
		}
	}

	if res.Handled {
		// The document is already on its way to an open viewer; all that is
		// left is to put that window in front of the user.
		focusViewer(res.Title)
		return nil
	}

	u := res.URL
	if hostOverride != "" {
		u = replaceHost(u, hostOverride, st.Port)
	}
	launchedApp, err := openViewer(u, mode, true)
	if err != nil {
		return err
	}
	if launchedApp {
		// The installed application opens on its own start page, so the
		// document has to be handed over once that window has connected.
		if deliverWhenReady(st, abs, 8*time.Second) {
			return nil
		}
		// Nothing connected: the shortcut is stale, or the app was uninstalled
		// behind our back. Rather than leave the double-click doing nothing,
		// fall back to opening a window directly.
		if _, err := openViewer(u, mode, false); err != nil {
			return err
		}
	}
	return nil
}

// deliverWhenReady waits for a viewer to appear and then hands it the document,
// reporting whether one took it.
func deliverWhenReady(st *state, abs string, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		res, err := requestOpen(st, abs, true)
		if err == nil && res.Handled {
			return true
		}
	}
	return false
}

// serveArgs is the command line a spawned background server is started with.
// It pins the port to whatever the last server managed to bind, so the origin
// an installed app was pinned to does not move underneath it.
func serveArgs() []string {
	args := []string{"serve"}
	if p := RememberedPort(); p > 0 {
		args = append(args, "--port", strconv.Itoa(p))
	}
	return args
}

// ensureServer returns a live server record, starting a detached one if needed.
func ensureServer() (*state, error) {
	if st, err := readState(); err == nil && st.alive() {
		return st, nil
	}

	locked, unlock := acquireSpawnLock()
	defer unlock()
	if !locked {
		// Another launcher is already starting the server; wait for it.
		if st, err := waitForServer(15 * time.Second); err == nil {
			return st, nil
		}
	} else if err := spawnServer(); err != nil {
		return nil, fmt.Errorf("could not start the viewer service: %w", err)
	}
	return waitForServer(10 * time.Second)
}

func waitForServer(limit time.Duration) (*state, error) {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		time.Sleep(120 * time.Millisecond)
		if st, err := readState(); err == nil && st.alive() {
			return st, nil
		}
	}
	return nil, fmt.Errorf("the viewer service did not start within %s", limit)
}

// acquireSpawnLock returns whether this process won the right to start the
// server. A lock left behind by a crash goes stale after 20 seconds.
func acquireSpawnLock() (bool, func()) {
	dir, err := stateDir()
	if err != nil {
		return true, func() {}
	}
	lock := filepath.Join(dir, "spawn.lock")

	if info, err := os.Stat(lock); err == nil && time.Since(info.ModTime()) > 20*time.Second {
		_ = os.Remove(lock)
	}
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return false, func() {}
	}
	_ = f.Close()
	return true, func() { _ = os.Remove(lock) }
}

// openResult is the server's answer to a launch request: either a URL to open
// a window on, or word that a running viewer has taken the document already.
type openResult struct {
	URL     string `json:"url"`
	Handled bool   `json:"handled"`
	Title   string `json:"title"`
	Error   string `json:"error"`
}

func requestOpen(st *state, abs string, reuse bool) (openResult, error) {
	var out openResult

	body, _ := json.Marshal(map[string]any{"path": abs, "reuse": reuse})
	req, err := http.NewRequest(http.MethodPost, st.controlURL("/open"), bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set(tokenHeader, st.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, err
	}
	if resp.StatusCode != http.StatusOK || (out.URL == "" && !out.Handled) {
		if out.Error != "" {
			return out, fmt.Errorf("%s", out.Error)
		}
		return out, fmt.Errorf("server returned status %d", resp.StatusCode)
	}
	return out, nil
}

func replaceHost(u, host string, port int) string {
	parsed, err := parseLoose(u)
	if err != nil {
		return u
	}
	parsed.Host = hostPort(host, port)
	return parsed.String()
}

// OpenHome shows the index of recently viewed files, reusing an open viewer
// when there is one.
func OpenHome(mode WindowMode) error {
	st, err := ensureServer()
	if err != nil {
		return err
	}
	if requestHome(st) {
		focusViewer("")
		return nil
	}
	key, err := requestKey(st)
	if err != nil {
		return err
	}
	_, err = openViewer(displayOrigin(st.Host, st.Port)+"/?k="+key, mode, true)
	return err
}

// EnsureRunning starts the server if it is not already up, and is what the
// md-localhost: URI handler needs: the offline page can then bring the viewer
// back without a terminal.
func EnsureRunning() error {
	_, err := ensureServer()
	return err
}

// requestHome asks a running viewer to show the index page, reporting whether
// one took it.
func requestHome(st *state) bool {
	body, _ := json.Marshal(map[string]any{"path": "", "home": true})
	req, err := http.NewRequest(http.MethodPost, st.controlURL("/home"), bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set(tokenHeader, st.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	var out struct {
		Handled bool `json:"handled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false
	}
	return out.Handled
}

// requestKey mints a single-use browser handoff key without registering a file.
func requestKey(st *state) (string, error) {
	req, err := http.NewRequest(http.MethodPost, st.controlURL("/key"), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set(tokenHeader, st.Token)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Key == "" {
		return "", fmt.Errorf("server did not return a session key")
	}
	return out.Key, nil
}
