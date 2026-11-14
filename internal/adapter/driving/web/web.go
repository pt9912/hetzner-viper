// Package web is the driving adapter of viper ui: a small web server on 127.0.0.1 that shows the machines, their logs and
// the result of viper check in the browser (docs/plan/ui.md). It guards every request as §3 of the plan says: the host name,
// a one-time key that becomes a session cookie, the origin of every POST and a strict content security policy.
package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Machine is one row of the overview.
type Machine struct {
	Name     string `json:"name"`
	Group    string `json:"group,omitempty"`
	Provider string `json:"provider,omitempty"`
	Type     string `json:"type,omitempty"`
	Location string `json:"location,omitempty"`
	NATS     string `json:"nats,omitempty"` // the NATS group
	// FileMax is the largest file its NATS group takes in bytes (NATS_OBJECTS_FILE_MAX), 0 without an object store.
	FileMax int64  `json:"file_max,omitempty"`
	State   string `json:"state,omitempty"` // ready, started or none: what the marks of this computer say
	Error   string `json:"error,omitempty"` // the settings that cannot be read
}

// Group is a group of groups/<name>.env.
type Group struct {
	Name     string   `json:"name"`
	Machines []string `json:"machines,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// NATSGroup is a group of groups/<name>.env whose machines are in a NATS group. Operator says that this computer has the key
// of the operator: viper nats then speaks as the operator, else as one of the machines (over ssh).
type NATSGroup struct {
	Name     string   `json:"name"`
	Machines []string `json:"machines"`
	Operator bool     `json:"operator"`
	// FileMax is the largest file the group takes, in bytes (NATS_OBJECTS_FILE_MAX); 0: the group has no object store.
	FileMax int64 `json:"file_max"`
}

// Setting is one line of a settings file: the name, and the value only for the names that are no credentials and no
// private data (the cli decides; everything else is just "set").
type Setting struct {
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
	Shown bool   `json:"shown"`
}

// SettingsFile is one file of settings, or the environment ("environment").
type SettingsFile struct {
	Name     string    `json:"name"`
	Settings []Setting `json:"settings"`
	Error    string    `json:"error,omitempty"`
}

// Overview is what viper knows of this computer's machines and groups, from the files alone.
type Overview struct {
	Machines []Machine   `json:"machines"`
	Groups   []Group     `json:"groups"`
	NATS     []NATSGroup `json:"nats"`
	Readonly bool        `json:"readonly"`
	Version  string      `json:"version"`
}

// Backend is what the server shows: the commands of viper, called in this process. The cli package gives it.
type Backend interface {
	// Overview lists the machines and groups. It asks no provider.
	Overview(ctx context.Context) (Overview, error)
	// Check runs viper check for the machine (in the group, if it has several) and returns the exit status.
	Check(ctx context.Context, machine, group string, out io.Writer) (int, error)
	// Settings reads the names of the settings files of the directory, and the values of those that are safe to show.
	Settings(ctx context.Context) ([]SettingsFile, error)
	// Log shows the log of the machine ("": the events of all machines).
	Log(ctx context.Context, machine string, full bool, out io.Writer) error
}

// Config is what makes a Server.
type Config struct {
	Backend  Backend
	Readonly bool
	Version  string
	// Clean makes an output fit to show (control characters, escape sequences), Redact takes the values of the settings
	// that are secrets out of it. nil: the output is shown as it is (tests only).
	Clean, Redact func(string) string
	// MaxUpload is the largest file a page may hand over to send (default 64 MiB; the limit of the group applies as well).
	MaxUpload int64
	// LongestSecret is the length of the longest value that Redact takes out (nil: 128). An open line is cut no nearer to its
	// end than that, so that a secret is never cut in two.
	LongestSecret func() int
	// UploadLife is how long a file that was handed over waits for its message (default 30 minutes).
	UploadLife time.Duration
	// Spawn starts viper for an action (nil: the pages offer none).
	Spawn Spawner
	// TokenTTL is how long after the start the one-time key can be exchanged (default one minute).
	TokenTTL time.Duration
	// Now is the clock (tests).
	Now func() time.Time
}

const (
	maxOutput      = 1 << 20 // what of a run is shown
	maxBody        = 4 << 10
	maxRunBody     = 64 << 10 // a message is up to 16 KiB, as JSON
	maxConcurrent  = 16
	maxStreams     = 8 // open event streams (a page has one)
	overviewLife   = time.Second
	checkTimeLimit = 15 * time.Minute
	cookiePrefix   = "viper_ui_"
)

// Server is the web server of viper ui.
type Server struct {
	cfg     Config
	host    string
	token   string
	cookie  string
	started time.Time

	mu       sync.Mutex
	used     bool
	checking map[string]bool // the machines a check runs for
	sem      chan struct{}
	runs     runTable
	uploads  uploadTable
	streams  chan struct{} // the budget of the open event streams: they must not starve cancel and answer
	quit     chan struct{} // closed when the server ends: the streams end
	quitOnce sync.Once

	exchanged     chan struct{} // closed when the key has been exchanged
	exchangedOnce sync.Once

	cookieName string // viper_ui_<port>: two servers on this computer do not overwrite each other's cookie

	ovMu sync.Mutex
	ov   Overview
	ovAt time.Time
	ovOK bool
}

// New makes a server. Listen gives it its address.
func New(cfg Config) *Server {
	if cfg.TokenTTL == 0 {
		cfg.TokenTTL = time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.MaxUpload == 0 {
		cfg.MaxUpload = 64 << 20
	}
	if cfg.UploadLife == 0 {
		cfg.UploadLife = 30 * time.Minute
	}
	if cfg.Clean == nil {
		cfg.Clean = func(s string) string { return s }
	}
	if cfg.Redact == nil {
		cfg.Redact = func(s string) string { return s }
	}
	return &Server{cfg: cfg, token: random(), cookie: random(), started: cfg.Now(), checking: map[string]bool{}, sem: make(chan struct{}, maxConcurrent), streams: make(chan struct{}, maxStreams), quit: make(chan struct{}), exchanged: make(chan struct{})}
}

func random() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // no randomness, no key: nothing sensible to do
	}
	return hex.EncodeToString(b)
}

// Listen opens 127.0.0.1:port (0: a free port). The server answers only to the name it is called by.
func (s *Server) Listen(port int) (net.Listener, error) {
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	s.host = ln.Addr().String()
	s.cookieName = cookiePrefix + strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	s.started = s.cfg.Now()
	return ln, nil
}

// Host is the address the server answers to (127.0.0.1:port).
func (s *Server) Host() string { return s.host }

// Token is the key that the first request exchanges for the session cookie.
func (s *Server) Token() string { return s.token }

// CookieName is the name of the session cookie (it has the port in it).
func (s *Server) CookieName() string { return s.cookieName }

// Serve answers until ctx ends, then asks the runs to end (as Ctrl-C does), closes the connections and waits for both, each for
// at most 20 seconds. It also cleans up when the listener fails.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 16 << 10}
	srv.RegisterOnShutdown(s.stop)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	var serveErr error
	select {
	case serveErr = <-done:
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
	case <-ctx.Done():
	}
	// the runs get their Ctrl-C at once, not after a long request has ended: the two waits are independent
	runsCtx, cancelRuns := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancelRuns()
	s.runs.close() // from here on no run is started: one that is being started at this moment is cancelled below
	ended := make(chan struct{})
	go func() { s.endRuns(runsCtx); close(ended) }()
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); errors.Is(err, context.DeadlineExceeded) {
		_ = srv.Close()
	}
	s.stop()
	<-ended
	s.endRuns(runsCtx) // a run that was started while the first round took its list
	s.uploads.removeAll()
	return serveErr
}

// stop ends the open streams (once).
func (s *Server) stop() { s.quitOnce.Do(func() { close(s.quit) }) }

// Exchanged is closed when the one-time key has been exchanged for the cookie.
func (s *Server) Exchanged() <-chan struct{} { return s.exchanged }

// snapshot is the overview, read at most once a second (every request that needs it would read every settings file again);
// a run that ends makes the next one read anew. The caller gets a copy it may change.
func (s *Server) snapshot(ctx context.Context) (Overview, error) {
	s.ovMu.Lock()
	defer s.ovMu.Unlock()
	if !s.ovOK || s.cfg.Now().Sub(s.ovAt) >= overviewLife {
		ov, err := s.cfg.Backend.Overview(ctx)
		if err != nil {
			return Overview{}, err
		}
		s.ov, s.ovAt, s.ovOK = ov, s.cfg.Now(), true
	}
	ov := s.ov
	ov.Machines = slices.Clone(ov.Machines)
	ov.Groups = slices.Clone(ov.Groups)
	ov.NATS = slices.Clone(ov.NATS)
	return ov, nil
}

// fresh reads the overview anew: what lets a command through (a machine that the files name) and what the output must not show
// (the secrets that the reading learns) is never a second old.
func (s *Server) fresh(ctx context.Context) (Overview, error) {
	s.forgetOverview()
	return s.snapshot(ctx)
}

func (s *Server) forgetOverview() {
	s.ovMu.Lock()
	s.ovOK = false
	s.ovMu.Unlock()
}

// Handler is the whole site, behind the guard.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /", site())
	mux.HandleFunc("GET /api/overview", s.overview)
	mux.HandleFunc("GET /api/log", s.log)
	mux.HandleFunc("GET /api/settings", s.settings)
	mux.HandleFunc("POST /api/check", s.check)
	mux.HandleFunc("GET /api/runs", s.listRuns)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("POST /api/run", s.startRun)
	mux.HandleFunc("POST /api/answer", s.answer)
	mux.HandleFunc("POST /api/cancel", s.cancelRun)
	mux.HandleFunc("POST /api/upload", s.upload)
	mux.HandleFunc("GET /api/download", s.download)
	return s.guard(mux)
}

func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setHeaders(w.Header())
		if s.host == "" || r.Host != s.host {
			deny(w, r)
			return
		}
		if t := r.URL.Query().Get("t"); t != "" && r.URL.Path == "/" && r.Method == http.MethodGet {
			s.exchange(w, r, t)
			return
		}
		if c, err := r.Cookie(s.cookieName); err != nil || !equal(c.Value, s.cookie) {
			deny(w, r)
			return
		}
		if r.Method == http.MethodPost && !s.sameOrigin(r) {
			deny(w, r)
			return
		}
		slot := s.sem
		if r.URL.Path == "/api/events" {
			slot = s.streams // an open stream must not take the place of a cancel
		}
		select {
		case slot <- struct{}{}:
			defer func() { <-slot }()
		default:
			http.Error(w, "too many requests", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func setHeaders(h http.Header) {
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
}

// deny answers 403. A browser that opens the address gets a page that says what to do (no style, no script: nothing here is
// served without the cookie); anything else gets the word.
func deny(w http.ResponseWriter, r *http.Request) {
	if wantsPage(r) {
		errorPage(w, http.StatusForbidden, "Not allowed / Nicht erlaubt",
			"Open viper ui with the address that viper printed in the terminal (the key in it works once). If you did, the session may have ended: start viper ui again.",
			"Öffnen Sie viper ui mit der Adresse, die viper im Terminal ausgegeben hat (der Schlüssel darin gilt nur einmal). Wenn Sie das getan haben, ist die Sitzung vielleicht zu Ende: starten Sie viper ui neu.")
		return
	}
	http.Error(w, "forbidden", http.StatusForbidden)
}

func wantsPage(r *http.Request) bool {
	return r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html") && !strings.HasPrefix(r.URL.Path, "/api/")
}

// errorPage is a page of plain HTML; it needs no other file (the page of a refusal cannot load any).
func errorPage(w http.ResponseWriter, code int, title, en, de string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>viper: %s</title></head><body><main><h1>%s</h1><p lang="en">%s</p><p lang="de">%s</p></main></body></html>`,
		html.EscapeString(title), html.EscapeString(title), html.EscapeString(en), html.EscapeString(de))
}

func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// sameOrigin is the check of a POST: the origin is this server, the header only a script of this server can add, and the
// content type is not one a form can send.
func (s *Server) sameOrigin(r *http.Request) bool {
	want := "application/json"
	if r.URL.Path == "/api/upload" {
		want = "application/octet-stream" // not a type a form can send: another origin needs a preflight, which gets no answer
	}
	if r.Header.Get("Origin") != "http://"+s.host || r.Header.Get("X-Viper-UI") != "1" || r.Header.Get("Content-Type") != want {
		return false
	}
	f := r.Header.Get("Sec-Fetch-Site")
	return f == "" || f == "same-origin"
}

// exchange gives the cookie for the one-time key: once, and in the first minute. The answer is a page that moves on to /.
func (s *Server) exchange(w http.ResponseWriter, r *http.Request, t string) {
	s.mu.Lock()
	ok := !s.used && s.cfg.Now().Sub(s.started) < s.cfg.TokenTTL && equal(t, s.token)
	if ok {
		s.used = true
	}
	s.mu.Unlock()
	if !ok {
		deny(w, r)
		return
	}
	s.exchangedOnce.Do(func() { close(s.exchanged) })
	http.SetCookie(w, &http.Cookie{Name: s.cookieName, Value: s.cookie, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
	// Not a redirect: when the key comes from a page of another origin (the start file is a file://), the browser sends the
	// requests of the whole redirect chain as cross-site and leaves out a SameSite=Strict cookie. A page of this server
	// that moves on by itself is a navigation of this site.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, `<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=/"><link rel="icon" type="image/svg+xml" href="/favicon.svg"><title>viper</title>`)
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ov, err := s.snapshot(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, s.show(err.Error()))
		return
	}
	ov.Readonly, ov.Version = s.cfg.Readonly, s.cfg.Version
	for i := range ov.Machines {
		ov.Machines[i].Error = s.show(ov.Machines[i].Error)
	}
	for i := range ov.Groups {
		ov.Groups[i].Error = s.show(ov.Groups[i].Error)
	}
	reply(w, ov)
}

// show makes a text fit for the page: clean, with the secrets taken out.
func (s *Server) show(text string) string { return s.cfg.Redact(s.cfg.Clean(text)) }

// known looks the machine up in the overview: only names that the files name get as far as a command.
func (s *Server) known(ctx context.Context, machine, group string) bool {
	ov, err := s.fresh(ctx)
	if err != nil {
		return false
	}
	for _, m := range ov.Machines {
		if m.Name == machine && m.Group == group && m.Error == "" {
			return true
		}
	}
	return false
}

func (s *Server) log(w http.ResponseWriter, r *http.Request) {
	machine, full := r.URL.Query().Get("machine"), r.URL.Query().Get("full") == "1"
	if machine == "" && full {
		fail(w, http.StatusBadRequest, "the full output needs a machine")
		return
	}
	if !s.knownName(r.Context(), machine) {
		fail(w, http.StatusNotFound, "no such machine")
		return
	}
	var out limited
	err := s.cfg.Backend.Log(r.Context(), machine, full, &out)
	text := s.show(out.String())
	if err != nil {
		text += "\n" + s.show(err.Error())
	}
	reply(w, map[string]any{"text": text, "truncated": out.cut})
}

// knownName is true for a machine that the files name, and for "" (the events of all). Either way it reads the overview,
// which is also where the backend learns the secrets of the settings that the output must not show.
func (s *Server) knownName(ctx context.Context, machine string) bool {
	ov, err := s.fresh(ctx)
	if err != nil {
		return false
	}
	if machine == "" {
		return true
	}
	for _, m := range ov.Machines {
		if m.Name == machine {
			return true
		}
	}
	return false
}

type checkRequest struct {
	Machine string `json:"machine"`
	Group   string `json:"group"`
}

func (s *Server) check(w http.ResponseWriter, r *http.Request) {
	var req checkRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "bad request")
		return
	}
	if !s.known(r.Context(), req.Machine, req.Group) {
		fail(w, http.StatusNotFound, "no such machine")
		return
	}
	key := req.Machine + "\x00" + req.Group
	s.mu.Lock()
	busy := s.checking[key]
	s.checking[key] = true
	s.mu.Unlock()
	if busy {
		fail(w, http.StatusConflict, "a check of this machine is running")
		return
	}
	defer func() { s.mu.Lock(); delete(s.checking, key); s.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(r.Context(), checkTimeLimit)
	defer cancel()
	var out limited
	code, err := s.cfg.Backend.Check(ctx, req.Machine, req.Group, &out)
	text := s.show(out.String())
	if err != nil {
		text += "\n" + s.show(err.Error())
		if code == 0 {
			code = 1
		}
	}
	reply(w, map[string]any{"code": code, "ok": code == 0, "output": text, "truncated": out.cut})
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// limited keeps the first maxOutput bytes of what is written to it and drops the rest.
type limited struct {
	mu  sync.Mutex
	b   []byte
	cut bool
}

func (l *limited) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if room := maxOutput - len(l.b); len(p) > room {
		l.b, l.cut = append(l.b, p[:max(room, 0)]...), true
	} else {
		l.b = append(l.b, p...)
	}
	return len(p), nil
}

func (l *limited) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return string(l.b)
}

// URL is the address of the server with the key, which the first request exchanges.
func (s *Server) URL() string { return fmt.Sprintf("http://%s/?t=%s", s.host, s.token) }

// site serves the pages of the program, and a page of its own for what is not there.
func site() http.Handler {
	files := http.FileServerFS(pages())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "."
		}
		if st, err := fs.Stat(pages(), name); err != nil || (st.IsDir() && name != ".") {
			errorPage(w, http.StatusNotFound, "Not found / Nicht gefunden", "There is no such page. Go back to the overview.", "Diese Seite gibt es nicht. Zurück zur Übersicht.")
			return
		}
		files.ServeHTTP(w, r)
	})
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	files, err := s.cfg.Backend.Settings(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, s.show(err.Error()))
		return
	}
	for i := range files {
		files[i].Error = s.show(files[i].Error)
		for j := range files[i].Settings {
			if files[i].Settings[j].Shown {
				files[i].Settings[j].Value = s.show(files[i].Settings[j].Value)
			} else {
				files[i].Settings[j].Value = ""
			}
		}
	}
	reply(w, map[string]any{"files": files})
}
