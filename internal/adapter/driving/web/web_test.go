package web_test

import (
	"context"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/web"
)

type fake struct {
	overview web.Overview
	checks   chan struct{} // a check waits for it, if set
	entered  chan struct{} // a check says that it began
	mu       sync.Mutex
	asked    []string
}

func (f *fake) Overview(context.Context) (web.Overview, error) { return f.overview, nil }

func (f *fake) Check(_ context.Context, machine, group string, out io.Writer) (int, error) {
	f.mu.Lock()
	f.asked = append(f.asked, machine+"/"+group)
	f.mu.Unlock()
	if f.entered != nil {
		f.entered <- struct{}{}
	}
	if f.checks != nil {
		<-f.checks
	}
	_, _ = io.WriteString(out, "token is s3cr3t-value-123 \x1b[31mred\x1b[0m\nCheck: 2 problem(s).\n")
	return 1, nil
}

func (f *fake) Settings(context.Context) ([]web.SettingsFile, error) {
	return []web.SettingsFile{{Name: ".env.local", Settings: []web.Setting{
		{Key: "SERVER_TYPE", Value: "cpx22", Shown: true},
		{Key: "HCLOUD_TOKEN", Value: "s3cr3t-value-123"}, // a backend that leaks: the server still drops the value
		{Key: "NOTE", Value: "s3cr3t-value-123", Shown: true},
	}}}, nil
}

func (f *fake) Log(_ context.Context, machine string, full bool, out io.Writer) error {
	_, _ = io.WriteString(out, "log of "+machine+" s3cr3t-value-123\n")
	return nil
}

func newFake() *fake {
	return &fake{overview: web.Overview{Machines: []web.Machine{{Name: "web", Provider: "hetzner"}, {Name: "bad", Error: "settings s3cr3t-value-123 wrong"}, {Name: "dev1", Group: "dev"}}}}
}

func clean(s string) string { return strings.ReplaceAll(s, "\x1b[31m", "") }

func redact(s string) string { return strings.ReplaceAll(s, "s3cr3t-value-123", "[hidden]") }

// started is a server with a port, and the clock the test sets.
func started(t *testing.T, f *fake, now *time.Time) (*web.Server, http.Handler) {
	t.Helper()
	cfg := web.Config{Backend: f, Version: "vtest", Clean: func(s string) string { return strings.ReplaceAll(clean(s), "\x1b[0m", "") }, Redact: redact}
	if now != nil {
		cfg.Now = func() time.Time { return *now }
	}
	s := web.New(cfg)
	ln, err := s.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return s, s.Handler()
}

func do(h http.Handler, method, host, path string, hdr map[string]string, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = host
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// login makes the exchange and returns the cookie header.
func login(t *testing.T, s *web.Server, h http.Handler) map[string]string {
	t.Helper()
	w := do(h, "GET", s.Host(), "/?t="+s.Token(), nil, "")
	if w.Code != http.StatusOK || w.Header().Get("Location") != "" || !strings.Contains(w.Body.String(), `http-equiv="refresh" content="0;url=/"`) {
		t.Fatalf("exchange: %d %v %s", w.Code, w.Header(), w.Body)
	}
	c := strings.SplitN(w.Header().Get("Set-Cookie"), ";", 2)[0]
	return map[string]string{"Cookie": c}
}

func post(s *web.Server, h http.Handler, cookie map[string]string, origin, ctype, fetch string) *httptest.ResponseRecorder {
	hdr := map[string]string{"Cookie": cookie["Cookie"], "Content-Type": ctype, "X-Viper-UI": "1"}
	if origin != "" {
		hdr["Origin"] = origin
	}
	if fetch != "" {
		hdr["Sec-Fetch-Site"] = fetch
	}
	return do(h, "POST", s.Host(), "/api/check", hdr, `{"machine":"web","group":""}`)
}

func TestTheHostMustBeTheAddressItselfNotLocalhost(t *testing.T) {
	s, h := started(t, newFake(), nil)
	_, port, _ := strings.Cut(s.Host(), ":")
	for _, host := range []string{"localhost:" + port, "evil.example:" + port, "127.0.0.1", "127.0.0.1:1", "[::1]:" + port, ""} {
		if w := do(h, "GET", host, "/?t="+s.Token(), nil, ""); w.Code != http.StatusForbidden {
			t.Errorf("host %q: %d", host, w.Code)
		}
	}
	// the key was not used up by the refused ones
	login(t, s, h)
}

func TestTheKeyWorksOnceAndOnlyAMinute(t *testing.T) {
	s, h := started(t, newFake(), nil)
	login(t, s, h)
	if w := do(h, "GET", s.Host(), "/?t="+s.Token(), nil, ""); w.Code != http.StatusForbidden {
		t.Errorf("second exchange: %d", w.Code)
	}
	now := time.Now()
	s2, h2 := started(t, newFake(), &now)
	now = now.Add(61 * time.Second)
	if w := do(h2, "GET", s2.Host(), "/?t="+s2.Token(), nil, ""); w.Code != http.StatusForbidden {
		t.Errorf("late exchange: %d", w.Code)
	}
	s3, h3 := started(t, newFake(), nil)
	if w := do(h3, "GET", s3.Host(), "/?t=wrong", nil, ""); w.Code != http.StatusForbidden {
		t.Errorf("wrong key: %d", w.Code)
	}
}

func TestTheExchangeCookieAndHeaders(t *testing.T) {
	s, h := started(t, newFake(), nil)
	w := do(h, "GET", s.Host(), "/?t="+s.Token(), nil, "")
	c := w.Header().Get("Set-Cookie")
	for _, want := range []string{"HttpOnly", "SameSite=Strict", "Path=/"} {
		if !strings.Contains(c, want) {
			t.Errorf("cookie %q lacks %s", c, want)
		}
	}
	for k, want := range map[string]string{"Referrer-Policy": "no-referrer", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "Cross-Origin-Opener-Policy": "same-origin", "Cross-Origin-Resource-Policy": "same-origin"} {
		if got := w.Header().Get(k); got != want {
			t.Errorf("%s = %q", k, got)
		}
	}
	csp := w.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "object-src 'none'", "frame-ancestors 'none'", "base-uri 'none'", "form-action 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("csp lacks %s: %s", want, csp)
		}
	}
	if strings.Contains(csp, "unsafe") {
		t.Errorf("csp: %s", csp)
	}
}

func TestWithoutTheCookieNothingIsAnswered(t *testing.T) {
	s, h := started(t, newFake(), nil)
	for _, p := range []string{"/", "/app.js", "/api/overview", "/api/log", "/nothing"} {
		if w := do(h, "GET", s.Host(), p, nil, ""); w.Code != http.StatusForbidden {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	if w := do(h, "GET", s.Host(), "/api/overview", map[string]string{"Cookie": "viper_ui=wrong"}, ""); w.Code != http.StatusForbidden {
		t.Errorf("wrong cookie: %d", w.Code)
	}
}

func TestAPostNeedsOriginHeaderAndContentType(t *testing.T) {
	s, h := started(t, newFake(), nil)
	c := login(t, s, h)
	own := "http://" + s.Host()
	if w := post(s, h, c, own, "application/json", "same-origin"); w.Code != 200 {
		t.Fatalf("good post: %d %s", w.Code, w.Body)
	}
	for name, w := range map[string]*httptest.ResponseRecorder{
		"no origin":      post(s, h, c, "", "application/json", ""),
		"null origin":    post(s, h, c, "null", "application/json", ""),
		"other origin":   post(s, h, c, "http://evil.example", "application/json", ""),
		"localhost":      post(s, h, c, strings.Replace(own, "127.0.0.1", "localhost", 1), "application/json", ""),
		"cross site":     post(s, h, c, own, "application/json", "cross-site"),
		"same site only": post(s, h, c, own, "application/json", "same-site"),
		"form type":      post(s, h, c, own, "application/x-www-form-urlencoded", ""),
		"text type":      post(s, h, c, own, "text/plain", ""),
	} {
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	// no header X-Viper-UI
	w := do(h, "POST", s.Host(), "/api/check", map[string]string{"Cookie": c["Cookie"], "Origin": own, "Content-Type": "application/json"}, `{"machine":"web"}`)
	if w.Code != http.StatusForbidden {
		t.Errorf("no X-Viper-UI: %d", w.Code)
	}
}

func TestTheMethodsAndPaths(t *testing.T) {
	s, h := started(t, newFake(), nil)
	c := login(t, s, h)
	if w := do(h, "GET", s.Host(), "/api/check", c, ""); w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET check: %d", w.Code)
	}
	if w := do(h, "POST", s.Host(), "/api/overview", c, ""); w.Code != http.StatusForbidden && w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST overview: %d", w.Code)
	}
	for _, p := range []string{"/nothing", "/../../etc/passwd", "/static/", "/.env.local", "/%2e%2e/secret"} {
		if w := do(h, "GET", s.Host(), p, c, ""); w.Code == http.StatusOK && !strings.Contains(w.Body.String(), "<title>viper</title>") {
			t.Errorf("%s: %d %q", p, w.Code, w.Body.String())
		} else if w.Code == http.StatusOK && p != "/static/" {
			t.Errorf("%s answered 200", p)
		}
	}
	if w := do(h, "GET", s.Host(), "/", c, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "<title>viper</title>") {
		t.Errorf("index: %d", w.Code)
	}
}

func TestCheckRunsOnlyForMachinesTheFilesName(t *testing.T) {
	f := newFake()
	s, h := started(t, f, nil)
	c := login(t, s, h)
	own := "http://" + s.Host()
	do2 := func(body string) *httptest.ResponseRecorder {
		return do(h, "POST", s.Host(), "/api/check", map[string]string{"Cookie": c["Cookie"], "Origin": own, "Content-Type": "application/json", "X-Viper-UI": "1"}, body)
	}
	for _, body := range []string{`{"machine":"--help"}`, `{"machine":"nope"}`, `{"machine":"web","group":"other"}`, `{"machine":"bad"}`, `{"machine":"web","extra":1}`, `not json`, `{"machine":"dev1"}`} {
		if w := do2(body); w.Code < 400 {
			t.Errorf("%s: %d", body, w.Code)
		}
	}
	if len(f.asked) != 0 {
		t.Errorf("the backend was called: %v", f.asked)
	}
	if w := do2(`{"machine":"dev1","group":"dev"}`); w.Code != 200 || len(f.asked) != 1 || f.asked[0] != "dev1/dev" {
		t.Errorf("group machine: %d %v", w.Code, f.asked)
	}
	if w := do2(strings.Repeat("x", 5000)); w.Code != http.StatusBadRequest {
		t.Errorf("a big body: %d", w.Code)
	}
}

func TestOutputsAreCleanedAndTheSecretsAreOut(t *testing.T) {
	s, h := started(t, newFake(), nil)
	c := login(t, s, h)
	w := post(s, h, c, "http://"+s.Host(), "application/json", "")
	b := w.Body.String()
	if strings.Contains(b, "s3cr3t") || strings.Contains(b, "\\u001b") || !strings.Contains(b, "[hidden]") || !strings.Contains(b, `"code":1`) || !strings.Contains(b, `"ok":false`) {
		t.Errorf("check: %s", b)
	}
	if w := do(h, "GET", s.Host(), "/api/log?machine=web", c, ""); strings.Contains(w.Body.String(), "s3cr3t") || !strings.Contains(w.Body.String(), "log of web [hidden]") {
		t.Errorf("log: %s", w.Body)
	}
	if w := do(h, "GET", s.Host(), "/api/log", c, ""); w.Code != 200 || strings.Contains(w.Body.String(), "s3cr3t") {
		t.Errorf("all events: %d %s", w.Code, w.Body)
	}
	if w := do(h, "GET", s.Host(), "/api/log?machine=zzz", c, ""); w.Code != http.StatusNotFound {
		t.Errorf("unknown machine: %d", w.Code)
	}
	if w := do(h, "GET", s.Host(), "/api/log?full=1", c, ""); w.Code != http.StatusBadRequest {
		t.Errorf("full of nothing: %d", w.Code)
	}
	ov := do(h, "GET", s.Host(), "/api/overview", c, "").Body.String()
	if strings.Contains(ov, "s3cr3t") || !strings.Contains(ov, `"version":"vtest"`) {
		t.Errorf("overview: %s", ov)
	}
}

func TestOneCheckPerMachineAtATime(t *testing.T) {
	f := newFake()
	f.checks, f.entered = make(chan struct{}), make(chan struct{}, 1)
	s, h := started(t, f, nil)
	c := login(t, s, h)
	first := make(chan int)
	go func() { first <- post(s, h, c, "http://"+s.Host(), "application/json", "").Code }()
	<-f.entered
	if w := post(s, h, c, "http://"+s.Host(), "application/json", ""); w.Code != http.StatusConflict {
		t.Errorf("second check: %d", w.Code)
	}
	close(f.checks)
	if code := <-first; code != 200 {
		t.Errorf("first check: %d", code)
	}
}

// The pages: no inline script or style, no eval, no innerHTML, nothing from elsewhere.
func TestThePagesHaveNothingThatTheModelForbids(t *testing.T) {
	n := 0
	err := fs.WalkDir(web.Pages(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		n++
		b, err := fs.ReadFile(web.Pages(), p)
		if err != nil {
			return err
		}
		s := strings.ReplaceAll(string(b), `xmlns="http://www.w3.org/2000/svg"`, "") // a name, not an address that is fetched
		for _, bad := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function", "setAttribute('style'", `setAttribute("style"`, " style=", "<style", "onclick=", "onerror=", "onload=", "http://", "https://", "//cdn", "@import", "url("} {
			if strings.Contains(s, bad) {
				t.Errorf("%s has %q", p, bad)
			}
		}
		if strings.HasSuffix(p, ".html") {
			for _, tag := range strings.Split(s, "<script")[1:] {
				if !strings.HasPrefix(strings.TrimSpace(tag), "src=") {
					t.Errorf("%s: a script without src", p)
				}
			}
		}
		return nil
	})
	if err != nil || n != 5 {
		t.Fatalf("%d files, %v", n, err)
	}
}

func TestServeEndsWithItsContext(t *testing.T) {
	s := web.New(web.Config{Backend: newFake()})
	ln, err := s.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the server did not end")
	}
}

func TestListenIsOnLoopbackOnly(t *testing.T) {
	s := web.New(web.Config{Backend: newFake()})
	ln, err := s.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	if !strings.HasPrefix(ln.Addr().String(), "127.0.0.1:") || s.Host() != ln.Addr().String() {
		t.Errorf("%s %s", ln.Addr(), s.Host())
	}
}

func TestTheSettingsPageShowsNamesAndOnlyTheValuesTheBackendShows(t *testing.T) {
	s, h := started(t, newFake(), nil)
	c := login(t, s, h)
	w := do(h, "GET", s.Host(), "/api/settings", c, "")
	b := w.Body.String()
	if w.Code != 200 || !strings.Contains(b, `"value":"cpx22"`) || strings.Contains(b, "s3cr3t") || !strings.Contains(b, `"key":"HCLOUD_TOKEN"`) {
		t.Errorf("settings: %d %s", w.Code, b)
	}
	if !strings.Contains(b, `"value":"[hidden]"`) { // a shown value is redacted like any output
		t.Errorf("redaction: %s", b)
	}
}

func TestARefusalAndAMissingPageAreAnswersAPersonCanRead(t *testing.T) {
	s, h := started(t, newFake(), nil)
	html := map[string]string{"Accept": "text/html,application/xhtml+xml"}
	w := do(h, "GET", s.Host(), "/", html, "")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "<h1>") || !strings.Contains(w.Body.String(), "viper printed") || !strings.Contains(w.Body.String(), "Terminal") {
		t.Errorf("refusal page: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "<script") || strings.Contains(w.Body.String(), "style") {
		t.Errorf("the page of a refusal loads nothing: %s", w.Body)
	}
	// the interface of programs stays plain
	if w := do(h, "GET", s.Host(), "/api/overview", html, ""); w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "<h1>") {
		t.Errorf("api refusal: %d %s", w.Code, w.Body)
	}
	c := login(t, s, h)
	for k, v := range html {
		c[k] = v
	}
	for _, p := range []string{"/nothing", "/static", "/app.js/x", "/%2e%2e/etc"} {
		if w := do(h, "GET", s.Host(), p, c, ""); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "<h1>") {
			t.Errorf("%s: %d %q", p, w.Code, w.Body)
		}
	}
	if w := do(h, "GET", s.Host(), "/", c, ""); w.Code != 200 {
		t.Errorf("index: %d", w.Code)
	}
}

func TestThePagesStaySmall(t *testing.T) {
	total := 0
	_ = fs.WalkDir(web.Pages(), ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := fs.ReadFile(web.Pages(), p)
			total += len(b)
		}
		return nil
	})
	if total > 120<<10 {
		t.Errorf("the pages are %d bytes: no build step, no library, so they stay small", total)
	}
}

// Every text of the page is in both languages, and every key that the page asks for is there.
func TestTheTwoLanguagesHaveTheSameKeysAndThePageAsksForNoOther(t *testing.T) {
	read := func(name string) string {
		b, err := fs.ReadFile(web.Pages(), name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	js := read("i18n.js")
	block := func(name string) map[string]bool {
		i := strings.Index(js, "const "+name+" = {")
		j := strings.Index(js[i:], "\n  };")
		if i < 0 || j < 0 {
			t.Fatalf("no dictionary %s", name)
		}
		keys := map[string]bool{}
		for _, m := range regexp.MustCompile(`'([a-z0-9.]+)':`).FindAllStringSubmatch(js[i:i+j], -1) {
			keys[m[1]] = true
		}
		return keys
	}
	en, de := block("en"), block("de")
	if len(en) < 80 {
		t.Fatalf("only %d keys: the test does not read the file", len(en))
	}
	for k := range en {
		if !de[k] {
			t.Errorf("%s is missing in German", k)
		}
	}
	for k := range de {
		if !en[k] {
			t.Errorf("%s is missing in English", k)
		}
	}
	asked := map[string]bool{}
	for _, m := range regexp.MustCompile(`data-i18n(?:-label)?="([a-z0-9.]+)"`).FindAllStringSubmatch(read("index.html"), -1) {
		asked[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`\bt\('([a-z0-9.]+)'`).FindAllStringSubmatch(read("app.js"), -1) {
		asked[m[1]] = true
	}
	for _, a := range []string{"preflight", "up", "down"} {
		for _, p := range []string{"btn.", "aria.", "btn.group.", "aria.group."} {
			asked[p+a] = true
		}
	}
	for _, k := range []string{"state.ready", "state.started", "state.none", "run.state.running", "run.state.waiting", "run.state.cancelling", "run.state.done"} {
		asked[k] = true
	}
	for k := range asked {
		if !en[k] && !strings.HasSuffix(k, ".") { // t('btn.' + name): the keys of that kind are listed above
			t.Errorf("the page asks for %s, which has no text", k)
		}
	}
}
