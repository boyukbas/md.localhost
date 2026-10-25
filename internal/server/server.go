package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/barisboyukbas/md.localhost/web"
)

const tokenHeader = "X-MDL-Token"
const cookieName = "mdl_session"

// Config controls a server instance.
type Config struct {
	PreferredPort int
	Host          string
	IdleMinutes   int
}

type recentEntry struct {
	Path string    `json:"path"`
	Name string    `json:"name"`
	Seen time.Time `json:"seen"`
}

type srv struct {
	cfg   Config
	port  int
	token string

	tmplPage  *template.Template
	tmplIndex *template.Template

	mu      sync.Mutex
	roots   map[string]bool      // directories the user has explicitly opened
	oneTime map[string]time.Time // single-use browser handoff keys
	recent  []recentEntry        // most recently opened files, newest first
	tabs    []tabEntry           // documents open in the viewer tab strip
	viewers map[*viewer]struct{} // pages holding an event stream

	sseCount     atomic.Int64
	lastActivity atomic.Int64 // unix nanos

	shutdown context.CancelFunc
}

// Serve runs the HTTP server until it is stopped or goes idle.
func Serve(cfg Config) error {
	if cfg.Host == "" {
		cfg.Host = DefaultHost
	}
	if cfg.PreferredPort == 0 {
		cfg.PreferredPort = DefaultPort
	}

	ln, port, err := listen(cfg.PreferredPort)
	if err != nil {
		return err
	}
	defer ln.Close()
	rememberPort(port)

	s := &srv{
		cfg:     cfg,
		port:    port,
		token:   LoadOrCreateToken(),
		roots:   map[string]bool{},
		oneTime: map[string]time.Time{},
		viewers: map[*viewer]struct{}{},
	}
	s.touch()
	s.loadRecent()
	closeLog := setupLogging()
	defer closeLog()

	s.tmplPage = template.Must(template.ParseFS(web.FS, "page.html"))
	s.tmplIndex = template.Must(template.ParseFS(web.FS, "index.html"))

	if err := writeState(&state{
		Port:    port,
		Host:    cfg.Host,
		PID:     os.Getpid(),
		Token:   s.token,
		Version: Version,
		Started: time.Now().Format(time.RFC3339),
	}); err != nil {
		return fmt.Errorf("writing state file: %w", err)
	}
	defer clearState()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.shutdown = cancel

	httpSrv := &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: server-sent events hold connections open.
		ErrorLog: log.New(log.Writer(), "http: ", log.LstdFlags),
	}

	go s.idleWatch(ctx)

	log.Printf("md.localhost %s listening on %s", Version, displayOrigin(cfg.Host, port))
	if port != cfg.PreferredPort {
		log.Printf("note: port %d was unavailable, using %d", cfg.PreferredPort, port)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Serve(ln) }()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutCtx, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = httpSrv.Shutdown(shutCtx)
		return nil
	}
}

// listen binds the preferred port, falling back to a range of alternatives so
// a busy port 80 (IIS, another dev server) never blocks startup.
func listen(preferred int) (net.Listener, int, error) {
	candidates := []int{preferred}
	for _, p := range []int{8080, 8088, 8181, 9080, 0} {
		if p != preferred {
			candidates = append(candidates, p)
		}
	}
	var lastErr error
	for _, p := range candidates {
		ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err == nil {
			return ln, ln.Addr().(*net.TCPAddr).Port, nil
		}
		lastErr = err
	}
	return nil, 0, fmt.Errorf("could not bind a local port: %w", lastErr)
}

func (s *srv) touch() { s.lastActivity.Store(time.Now().UnixNano()) }

// idleWatch exits the process once no viewer has been connected for a while,
// so the server does not linger forever after the last tab is closed.
func (s *srv) idleWatch(ctx context.Context) {
	if s.cfg.IdleMinutes <= 0 {
		return
	}
	limit := time.Duration(s.cfg.IdleMinutes) * time.Minute
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if s.sseCount.Load() > 0 {
				s.touch()
				continue
			}
			if time.Since(time.Unix(0, s.lastActivity.Load())) > limit {
				log.Printf("idle for %s with no open viewers, shutting down", limit)
				s.shutdown()
				return
			}
		}
	}
}

func (s *srv) routes() http.Handler {
	mux := http.NewServeMux()

	// Public: no authentication, no filesystem access.
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.HandlerFunc(s.handleAsset)))
	mux.HandleFunc("/favicon.ico", s.handleFavicon)
	// The browser fetches these three itself, without the page's cookie.
	mux.HandleFunc("/manifest.webmanifest", s.handleManifest)
	mux.HandleFunc("/sw.js", s.handleServiceWorker)
	mux.HandleFunc("/offline.html", s.handleOffline)

	// Control plane: requires the shared token from the state file.
	mux.HandleFunc("/open", s.requireToken(s.handleOpen))
	mux.HandleFunc("/home", s.requireToken(s.handleOpenHome))
	mux.HandleFunc("/quit", s.requireToken(s.handleQuit))
	mux.HandleFunc("/key", s.requireToken(s.handleKey))

	// Browser plane: requires the session cookie.
	mux.HandleFunc("/view/", s.requireSession(s.handleView))
	mux.HandleFunc("/events", s.requireSession(s.handleEvents))
	mux.HandleFunc("/tabs.json", s.requireSession(s.handleTabs))
	mux.HandleFunc("/tabs/close", s.requireSession(s.handleTabClose))
	mux.HandleFunc("/viewer/focus", s.requireSession(s.handleViewerFocus))
	mux.HandleFunc("/", s.requireSession(s.handleIndex))

	return s.baseMiddleware(mux)
}

// baseMiddleware pins the Host header and records activity. Pinning Host
// defeats DNS rebinding, where a hostile page points its own domain at
// 127.0.0.1 to read local files through this server.
func (s *srv) baseMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.ToLower(strings.Trim(host, "[]"))
		switch host {
		case s.cfg.Host, "md.localhost", "localhost", "127.0.0.1", "::1":
		default:
			http.Error(w, "unrecognised Host header", http.StatusMisdirectedRequest)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// same-origin, not no-referrer: this server uses the Referer to decide
		// that a link was followed from a page it served itself — which is how
		// browsing between documents authorises their folders, and how the
		// offline page gets back to the index. Nothing is ever sent to another
		// origin, which is the part that mattered.
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		s.touch()
		next.ServeHTTP(w, r)
	})
}

func (s *srv) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if subtleCompare(r.Header.Get(tokenHeader), s.token) {
			next(w, r)
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
	}
}

// requireSession accepts either a valid session cookie or a single-use handoff
// key, and refuses requests initiated by another site.
func (s *srv) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
			http.Error(w, "cross-site requests are not allowed", http.StatusForbidden)
			return
		}
		if c, err := r.Cookie(cookieName); err == nil && subtleCompare(c.Value, s.token) {
			next(w, r)
			return
		}
		// The index page is also reachable by the user simply opening it: a
		// taskbar icon, a bookmark, the installed app launching its start URL.
		// Those carry no handoff key, and cannot be produced by another site.
		if r.URL.Path == "/" && (isUserNavigation(r) || isOwnPageNavigation(r)) {
			http.SetCookie(w, sessionCookie(s.token))
			next(w, r)
			return
		}
		if k := r.URL.Query().Get("k"); k != "" && s.consumeOneTime(k) {
			http.SetCookie(w, sessionCookie(s.token))
			// Redirect to the same URL without the key so it does not linger
			// in the address bar or browser history.
			q := r.URL.Query()
			q.Del("k")
			clean := *r.URL
			clean.RawQuery = q.Encode()
			clean.Scheme, clean.Host = "", ""
			http.Redirect(w, r, clean.String(), http.StatusSeeOther)
			return
		}
		http.Error(w, "this page must be opened with the md.localhost launcher", http.StatusForbidden)
	}
}

// sessionCookie is the browser's proof that a page came from the launcher or
// from the user's own hand. It outlives the browser session so that a pinned
// application still opens after a reboot.
func sessionCookie(token string) *http.Cookie {
	return &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   365 * 24 * 60 * 60,
	}
}

func (s *srv) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"version": Version,
		"port":    s.port,
		"host":    s.cfg.Host,
	})
}

func (s *srv) handleQuit(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	go func() {
		time.Sleep(150 * time.Millisecond)
		s.shutdown()
	}()
}

// handleOpen is called by `mdlocalhost open`. It authorises the file's folder,
// mints a one-time handoff key and returns the URL for the browser.
func (s *srv) handleOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path  string `json:"path"`
		Reuse *bool  `json:"reuse"` // absent means yes
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	abs, err := filepath.Abs(req.Path)
	if err != nil {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	if _, err := os.Stat(abs); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	s.authorize(filepath.Dir(abs))
	s.addRecent(abs)
	s.addTab(abs)
	s.broadcast(busMsg{Event: "tabs", Data: "1"})

	// A viewer that is already open takes the document as another tab of its
	// own, so double-clicking a file does not add a browser tab every time.
	if req.Reuse == nil || *req.Reuse {
		if s.dispatchOpen(viewPath(abs)) {
			writeJSON(w, http.StatusOK, map[string]any{
				"handled": true,
				"title":   filepath.Base(abs),
				"port":    s.port,
			})
			return
		}
	}

	key := newToken()
	s.mu.Lock()
	s.oneTime[key] = time.Now().Add(60 * time.Second)
	s.mu.Unlock()

	u := viewURL(s.cfg.Host, s.port, abs) + "?k=" + url.QueryEscape(key)
	writeJSON(w, http.StatusOK, map[string]any{"url": u, "port": s.port})
}

// handleOpenHome asks a running viewer to show the index page.
func (s *srv) handleOpenHome(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"handled": s.dispatchOpen("/")})
}

// handleTabs reports the open tabs so a page can refresh its strip without
// reloading the document it is showing.
func (s *srv) handleTabs(w http.ResponseWriter, r *http.Request) {
	active := ""
	if raw := r.URL.Query().Get("active"); raw != "" {
		if t, err := urlPathToPath(raw); err == nil {
			active = filepath.Clean(t)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tabs": s.tabViews(active)})
}

// handleTabClose removes one document from the strip and says which tab should
// take its place.
func (s *srv) handleTabClose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || req.Path == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	next := s.closeTab(filepath.Clean(req.Path))
	s.broadcast(busMsg{Event: "tabs", Data: "1"})
	writeJSON(w, http.StatusOK, map[string]any{"next": next})
}

// handleViewerFocus records which page the user is looking at, so the next
// document opened from Explorer lands there rather than in a stale window.
func (s *srv) handleViewerFocus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.noteFocus(req.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleKey mints a single-use browser handoff key with no file attached.
func (s *srv) handleKey(w http.ResponseWriter, r *http.Request) {
	key := newToken()
	s.mu.Lock()
	s.oneTime[key] = time.Now().Add(60 * time.Second)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"key": key})
}

func (s *srv) consumeOneTime(k string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.oneTime[k]
	if !ok {
		return false
	}
	delete(s.oneTime, k)
	for key, e := range s.oneTime { // opportunistic sweep
		if time.Now().After(e) {
			delete(s.oneTime, key)
		}
	}
	return time.Now().Before(exp)
}

func (s *srv) authorize(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roots[filepath.Clean(dir)] = true
}

func (s *srv) authorized(target string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for root := range s.roots {
		if withinRoot(root, target) {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// subtleCompare is a constant-time string comparison.
func subtleCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// ---------------------------------------------------------------- assets

func (s *srv) handleAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+r.URL.Path)), "/")
	if name == "" || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}

	data, err := web.FS.ReadFile("assets/" + name)
	gzipped := false
	if err != nil {
		if data, err = web.FS.ReadFile("assets/" + name + ".gz"); err != nil {
			http.NotFound(w, r)
			return
		}
		gzipped = true
	}

	ct := mime.TypeByExtension(filepath.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if gzipped {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

func (s *srv) handleFavicon(w http.ResponseWriter, r *http.Request) {
	data, err := web.FS.ReadFile("assets/favicon.svg")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}

// ---------------------------------------------------------------- recents

func (s *srv) recentPath() string {
	dir, err := stateDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "recent.json")
}

func (s *srv) loadRecent() {
	p := s.recentPath()
	if p == "" {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &s.recent)
}

func (s *srv) addRecent(abs string) {
	s.mu.Lock()
	out := []recentEntry{{Path: abs, Name: filepath.Base(abs), Seen: time.Now()}}
	for _, e := range s.recent {
		if !strings.EqualFold(e.Path, abs) {
			out = append(out, e)
		}
		if len(out) >= 25 {
			break
		}
	}
	s.recent = out
	snapshot := append([]recentEntry(nil), out...)
	s.mu.Unlock()

	if p := s.recentPath(); p != "" {
		if b, err := json.MarshalIndent(snapshot, "", "  "); err == nil {
			_ = os.WriteFile(p, b, 0o600)
		}
	}
}

func (s *srv) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	entries := append([]recentEntry(nil), s.recent...)
	s.mu.Unlock()

	sort.Slice(entries, func(i, j int) bool { return entries[i].Seen.After(entries[j].Seen) })

	type row struct {
		Name, Dir, URL string
		Missing        bool
	}
	rows := make([]row, 0, len(entries))
	for _, e := range entries {
		_, err := os.Stat(e.Path)
		rows = append(rows, row{
			Name:    e.Name,
			Dir:     filepath.Dir(e.Path),
			URL:     viewPath(e.Path),
			Missing: err != nil,
		})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmplIndex.Execute(w, map[string]any{
		"Version":     Version,
		"TitleSuffix": titleSuffix,
		"Origin":      displayOrigin(s.cfg.Host, s.port),
		"Recent":      rows,
		"Tabs":        s.tabViews(""),
	}); err != nil {
		log.Printf("index template: %v", err)
	}
}

// ---------------------------------------------------------------- viewing

func (s *srv) handleView(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/view/")
	target, err := urlPathToPath(rest)
	if err != nil {
		http.Error(w, "bad path: "+err.Error(), http.StatusBadRequest)
		return
	}
	target = filepath.Clean(target)

	// A link followed from a page we already serve implicitly authorises its
	// folder, so browsing between documents works; a bare URL typed or probed
	// from elsewhere does not.
	if !s.authorized(target) {
		ref := r.Header.Get("Referer")
		switch {
		case ref != "" && s.refererAuthorized(ref):
			s.authorize(filepath.Dir(target))
		case ref != "" && refererIsIndex(ref) && s.isRecent(target):
			// The allow-list is per-session, so after a restart the recents
			// list would be a page of dead links. Clicking one is the user
			// naming that file again, and only files they opened before are
			// on it.
			s.authorize(filepath.Dir(target))
		default:
			http.Error(w, "that folder has not been opened in this session", http.StatusForbidden)
			return
		}
	}

	info, err := os.Stat(target)
	if err != nil {
		http.Error(w, "cannot open file: "+err.Error(), http.StatusNotFound)
		return
	}
	if info.IsDir() {
		s.serveDirectory(w, r, target)
		return
	}
	if !isMarkdown(target) {
		// Images, stylesheets, downloads referenced from a document.
		http.ServeFile(w, r, target)
		return
	}

	src, err := os.ReadFile(target)
	if err != nil {
		http.Error(w, "cannot read file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	doc, err := renderMarkdown(src)
	if err != nil {
		http.Error(w, "cannot render file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	title := doc.Title
	if title == "" {
		title = filepath.Base(target)
	}

	// Opening a document puts it in the strip; closing its tab takes it out.
	s.addTab(target)
	s.broadcast(busMsg{Event: "tabs", Data: "1"})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	data := map[string]any{
		"Title":       title,
		"TitleSuffix": titleSuffix,
		"FileName":    filepath.Base(target),
		"Dir":         filepath.Dir(target),
		"FullPath":    target,
		"Body":        template.HTML(doc.HTML), //nolint:gosec // local file, rendered by request
		"FrontMatter": doc.FrontMatter,
		"EventsURL":   "/events?p=" + url.QueryEscape(pathToURLPath(target)),
		"ParentURL":   viewPath(filepath.Dir(target)),
		"Modified":    info.ModTime().Format("2006-01-02 15:04"),
		"Version":     Version,
		"Tabs":        s.tabViews(target),
	}
	if err := s.tmplPage.Execute(w, data); err != nil {
		log.Printf("page template: %v", err)
	}
}

// refererIsIndex reports whether a request came from the recents page.
func refererIsIndex(ref string) bool {
	u, err := url.Parse(ref)
	if err != nil {
		return false
	}
	return u.Path == "/" || u.Path == ""
}

// isRecent reports whether a path is one the user has opened before, which is
// what the recents page lists.
func (s *srv) isRecent(target string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.recent {
		if strings.EqualFold(e.Path, target) {
			return true
		}
	}
	return false
}

func (s *srv) refererAuthorized(ref string) bool {
	u, err := url.Parse(ref)
	if err != nil || !strings.HasPrefix(u.Path, "/view/") {
		return false
	}
	p, err := urlPathToPath(strings.TrimPrefix(u.Path, "/view/"))
	if err != nil {
		return false
	}
	return s.authorized(filepath.Clean(p))
}

// serveDirectory lists Markdown files and subfolders so a link to a folder
// does something useful instead of erroring.
func (s *srv) serveDirectory(w http.ResponseWriter, r *http.Request, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		http.Error(w, "cannot list folder: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var b strings.Builder
	b.WriteString("# " + filepath.Base(dir) + "\n\n")
	b.WriteString("`" + dir + "`\n\n")
	var dirs, files []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		link := (&url.URL{Path: e.Name()}).String()
		if e.IsDir() {
			dirs = append(dirs, "- ["+e.Name()+"/]("+link+"/)")
		} else if isMarkdown(e.Name()) {
			files = append(files, "- ["+e.Name()+"]("+link+")")
		}
	}
	if len(dirs) > 0 {
		b.WriteString("## Folders\n\n" + strings.Join(dirs, "\n") + "\n\n")
	}
	if len(files) > 0 {
		b.WriteString("## Markdown files\n\n" + strings.Join(files, "\n") + "\n")
	}
	if len(dirs) == 0 && len(files) == 0 {
		b.WriteString("_No Markdown files here._\n")
	}

	doc, err := renderMarkdown([]byte(b.String()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = s.tmplPage.Execute(w, map[string]any{
		"Title":       filepath.Base(dir),
		"TitleSuffix": titleSuffix,
		"FileName":    filepath.Base(dir),
		"Dir":         filepath.Dir(dir),
		"FullPath":    dir,
		"EventsURL":   "/events",
		"Body":        template.HTML(doc.HTML), //nolint:gosec // generated above
		"ParentURL":   viewPath(filepath.Dir(dir)),
		"Version":     Version,
		"Tabs":        s.tabViews(""),
	})
}

// unusedFS keeps the fs import meaningful if the asset walk is removed.
var _ fs.FS = web.FS
