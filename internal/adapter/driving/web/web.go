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
	"io"
	"net"
	"net/http"
	"strconv"
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
	NATS     string `json:"nats,omitempty"`  // the NATS group
	State    string `json:"state,omitempty"` // ready, started or none: what the marks of this computer say
	Error    string `json:"error,omitempty"` // the settings that cannot be read
}

// Group is a group of groups/<name>.env.
type Group struct {
	Name     string   `json:"name"`
	Machines []string `json:"machines,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// Overview is what viper knows of this computer's machines and groups, from the files alone.
type Overview struct {
	Machines []Machine `json:"machines"`
	Groups   []Group   `json:"groups"`
	Readonly bool      `json:"readonly"`
	Version  string    `json:"version"`
}

// Backend is what the server shows: the commands of viper, called in this process. The cli package gives it.
type Backend interface {
	// Overview lists the machines and groups. It asks no provider.
	Overview(ctx context.Context) (Overview, error)
	// Check runs viper check for the machine (in the group, if it has several) and returns the exit status.
	Check(ctx context.Context, machine, group string, out io.Writer) (int, error)
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
	maxConcurrent  = 16
	checkTimeLimit = 15 * time.Minute
	cookieName     = "viper_ui"
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
	quit     chan struct{} // closed when the server ends: the streams end
}

// New makes a server. Listen gives it its address.
func New(cfg Config) *Server {
	if cfg.TokenTTL == 0 {
		cfg.TokenTTL = time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Clean == nil {
		cfg.Clean = func(s string) string { return s }
	}
	if cfg.Redact == nil {
		cfg.Redact = func(s string) string { return s }
	}
	return &Server{cfg: cfg, token: random(), cookie: random(), started: cfg.Now(), checking: map[string]bool{}, sem: make(chan struct{}, maxConcurrent), quit: make(chan struct{})}
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
	s.started = s.cfg.Now()
	return ln, nil
}

// Host is the address the server answers to (127.0.0.1:port).
func (s *Server) Host() string { return s.host }

// Token is the key that the first request exchanges for the session cookie.
func (s *Server) Token() string { return s.token }

// Serve answers until ctx ends, then closes the connections after at most 20 seconds.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 16 << 10}
	srv.RegisterOnShutdown(func() { close(s.quit) })
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	err := srv.Shutdown(sctx)
	s.endRuns(sctx)
	if errors.Is(err, context.DeadlineExceeded) {
		_ = srv.Close()
	}
	return nil
}

// Handler is the whole site, behind the guard.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(pages()))
	mux.HandleFunc("GET /api/overview", s.overview)
	mux.HandleFunc("GET /api/log", s.log)
	mux.HandleFunc("POST /api/check", s.check)
	mux.HandleFunc("GET /api/runs", s.listRuns)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("POST /api/run", s.startRun)
	mux.HandleFunc("POST /api/answer", s.answer)
	mux.HandleFunc("POST /api/cancel", s.cancelRun)
	return s.guard(mux)
}

func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setHeaders(w.Header())
		if s.host == "" || r.Host != s.host {
			deny(w)
			return
		}
		if t := r.URL.Query().Get("t"); t != "" && r.URL.Path == "/" && r.Method == http.MethodGet {
			s.exchange(w, r, t)
			return
		}
		if c, err := r.Cookie(cookieName); err != nil || !equal(c.Value, s.cookie) {
			deny(w)
			return
		}
		if r.Method == http.MethodPost && !s.sameOrigin(r) {
			deny(w)
			return
		}
		select {
		case s.sem <- struct{}{}:
			defer func() { <-s.sem }()
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

func deny(w http.ResponseWriter) { http.Error(w, "forbidden", http.StatusForbidden) }

func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// sameOrigin is the check of a POST: the origin is this server, the header only a script of this server can add, and the
// content type is not one a form can send.
func (s *Server) sameOrigin(r *http.Request) bool {
	if r.Header.Get("Origin") != "http://"+s.host || r.Header.Get("X-Viper-UI") != "1" || r.Header.Get("Content-Type") != "application/json" {
		return false
	}
	f := r.Header.Get("Sec-Fetch-Site")
	return f == "" || f == "same-origin"
}

// exchange gives the cookie for the one-time key: once, and in the first minute.
func (s *Server) exchange(w http.ResponseWriter, r *http.Request, t string) {
	s.mu.Lock()
	ok := !s.used && s.cfg.Now().Sub(s.started) < s.cfg.TokenTTL && equal(t, s.token)
	if ok {
		s.used = true
	}
	s.mu.Unlock()
	if !ok {
		deny(w)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: s.cookie, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ov, err := s.cfg.Backend.Overview(r.Context())
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
	ov, err := s.cfg.Backend.Overview(ctx)
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
	ov, err := s.cfg.Backend.Overview(ctx)
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
