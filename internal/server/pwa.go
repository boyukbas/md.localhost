package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/barisboyukbas/md.localhost/web"
)

// AppName is what the installed application is called: the window title suffix,
// the manifest name, and the name of the shortcut the browser writes when the
// viewer is installed.
const AppName = "md.localhost"

// The manifest, the service worker and the offline page are served without a
// session cookie, because the browser fetches all three anonymously: a
// <link rel="manifest"> request omits credentials unless the tag asks for them,
// and both the worker script and the icons are fetched by the browser rather
// than by the page. None of them reveals anything about the filesystem — they
// are the same three static files for every user of this program.

// handleManifest serves the web app manifest that makes the viewer installable
// as a desktop application, which is what allows it to be pinned to the taskbar
// with its own icon and window.
func (s *srv) handleManifest(w http.ResponseWriter, r *http.Request) {
	manifest := map[string]any{
		// id is what the browser uses to recognise an already-installed app.
		// It resolves against the origin, so it survives everything except a
		// change of port — see the note in the README.
		"id":               "/",
		"name":             AppName,
		"short_name":       AppName,
		"description":      "Markdown files from this machine, rendered.",
		"start_url":        "/",
		"scope":            "/",
		"display":          "standalone",
		"display_override": []string{"standalone", "minimal-ui"},
		"theme_color":      "#2b7fff",
		"background_color": "#ffffff",
		"categories":       []string{"productivity", "utilities"},
		// Clicking the taskbar icon while the viewer is already open brings
		// that window forward instead of opening a second one.
		"launch_handler": map[string]any{"client_mode": "focus-existing"},
		"icons": []map[string]any{
			{"src": "/assets/icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any"},
			{"src": "/assets/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any"},
			{"src": "/assets/icon-maskable-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable"},
		},
	}

	// Not writeJSON: a manifest is served as application/manifest+json, and
	// writeJSON would stamp application/json over it.
	w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(manifest)
}

// handleServiceWorker serves the worker from the site root so that its scope
// covers every page; a worker served from /assets/ could only control /assets/.
func (s *srv) handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	data, err := web.FS.ReadFile("assets/sw.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// The offline page is inlined into the worker as well as cached by it, so
	// that a browser profile where CacheStorage is unavailable still gets the
	// viewer's own page rather than a browser error.
	if page, err := web.FS.ReadFile("assets/offline.html"); err == nil {
		if quoted, err := json.Marshal(string(page)); err == nil {
			data = bytes.Replace(data, []byte(`"__OFFLINE_HTML__"`), quoted, 1)
		}
	}

	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Service-Worker-Allowed", "/")
	// The worker must be refetched rather than served from the HTTP cache, or a
	// new build would keep the previous worker alive.
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

// handleOffline serves the page the worker shows when the server is not
// running, which is the state a pinned taskbar icon can be clicked in.
func (s *srv) handleOffline(w http.ResponseWriter, r *http.Request) {
	data, err := web.FS.ReadFile("assets/offline.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

// isUserNavigation reports whether a request is the browser opening a page
// because the user asked it to — a typed address, a bookmark, a taskbar icon,
// an installed app launching its start URL.
//
// This is the one case where a page may be served without the launcher's
// handoff key, and it is safe because another site cannot produce it: a link
// or a script navigation from a different origin arrives as Sec-Fetch-Site:
// cross-site, and a subresource or fetch is not Sec-Fetch-Dest: document.
// Measured against Chromium 148 rather than assumed.
func isUserNavigation(r *http.Request) bool {
	return isTopLevelNavigation(r) && strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "none")
}

// isOwnPageNavigation reports whether one of this server's own pages sent the
// browser here. The offline page needs it: once the server is back it sends the
// window to the index, and a script navigation is same-origin rather than
// none, so it would otherwise be refused in a browser that has no cookie yet.
//
// A framed or fetched request is not Sec-Fetch-Dest: document, and a page on
// another origin navigating this window arrives as cross-site — so this stays
// out of reach of any other site, which cannot forge a Referer either.
func isOwnPageNavigation(r *http.Request) bool {
	if !isTopLevelNavigation(r) || !strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "same-origin") {
		return false
	}
	ref, err := url.Parse(r.Header.Get("Referer"))
	if err != nil || ref.Host == "" {
		return false
	}
	return strings.EqualFold(ref.Host, r.Host)
}

func isTopLevelNavigation(r *http.Request) bool {
	return r.Method == http.MethodGet &&
		strings.EqualFold(r.Header.Get("Sec-Fetch-Mode"), "navigate") &&
		strings.EqualFold(r.Header.Get("Sec-Fetch-Dest"), "document")
}
