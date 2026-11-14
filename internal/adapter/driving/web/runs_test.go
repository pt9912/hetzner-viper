package web_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/web"
)

// proc is a viper command that the test drives: it writes output, reads the answers and ends when told.
type proc struct {
	outR       *io.PipeReader
	outW       *io.PipeWriter
	inR        *io.PipeReader
	inW        *io.PipeWriter
	code       chan int
	interrupts chan struct{}
	killed     chan struct{}
	argv       []string
}

func newProc(argv []string) *proc {
	p := &proc{code: make(chan int, 1), interrupts: make(chan struct{}, 4), killed: make(chan struct{}, 1), argv: argv}
	p.outR, p.outW = io.Pipe()
	p.inR, p.inW = io.Pipe()
	return p
}

func (p *proc) Output() io.Reader { return p.outR }
func (p *proc) Input() io.Writer  { return p.inW }
func (p *proc) Interrupt() error  { p.interrupts <- struct{}{}; return nil }
func (p *proc) Kill() error       { p.killed <- struct{}{}; p.end(137); return nil }
func (p *proc) Wait() int         { return <-p.code }
func (p *proc) say(s string)      { _, _ = io.WriteString(p.outW, s) }
func (p *proc) end(code int)      { _ = p.outW.Close(); p.code <- code }

// spawns collects the processes the server started.
type spawns struct {
	mu    sync.Mutex
	procs []*proc
	ch    chan *proc
}

func (s *spawns) spawn(argv []string) (*proc, error) {
	p := newProc(argv)
	s.mu.Lock()
	s.procs = append(s.procs, p)
	s.mu.Unlock()
	s.ch <- p
	return p, nil
}

func (s *spawns) next(t *testing.T) *proc {
	t.Helper()
	select {
	case p := <-s.ch:
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("no process started")
		return nil
	}
}

// site is a server that really listens, and a client that went through the exchange.
type site struct {
	s     *web.Server
	c     *http.Client
	base  string
	spawn *spawns
}

func live(t *testing.T, readonly bool) *site {
	t.Helper()
	f := newFake()
	f.overview.Groups = []web.Group{{Name: "dev", Machines: []string{"dev1", "dev2"}}}
	f.overview.Machines = append(f.overview.Machines, web.Machine{Name: "dev2", Group: "dev"})
	sp := &spawns{ch: make(chan *proc, 8)}
	cfg := web.Config{Backend: f, Readonly: readonly, Redact: redact, Spawn: func(argv []string) (web.Proc, error) { return sp.spawn(argv) }}
	s := web.New(cfg)
	ln, err := s.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Serve(ctx, ln); close(done) }()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	st := &site{s: s, c: c, base: "http://" + s.Host(), spawn: sp}
	resp, err := c.Get(s.URL())
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatal(err, resp)
	}
	_ = resp.Body.Close()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Error("the server did not end")
		}
	})
	return st
}

func (st *site) post(t *testing.T, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", st.base+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Viper-UI", "1")
	req.Header.Set("Origin", st.base)
	resp, err := st.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (st *site) start(t *testing.T, body string) (string, int) {
	t.Helper()
	code, b := st.post(t, "/api/run", body)
	var r struct{ ID string }
	_ = json.Unmarshal([]byte(b), &r)
	return r.ID, code
}

// stream is what the page sees of a run.
type stream struct {
	events chan [2]string // kind, data
	cancel context.CancelFunc
}

func (st *site) attach(t *testing.T, id string) *stream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", st.base+"/api/events?id="+id, nil)
	resp, err := st.c.Do(req) //nolint:bodyclose // the goroutine below reads and closes it
	if err != nil || resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal(err, resp)
	}
	s := &stream{events: make(chan [2]string, 256), cancel: cancel}
	go func() {
		defer func() { _ = resp.Body.Close(); close(s.events) }()
		sc := bufio.NewScanner(resp.Body)
		kind := ""
		for sc.Scan() {
			l := sc.Text()
			switch {
			case strings.HasPrefix(l, "event: "):
				kind = l[7:]
			case strings.HasPrefix(l, "data: "):
				s.events <- [2]string{kind, l[6:]}
			}
		}
	}()
	t.Cleanup(cancel)
	return s
}

// until reads events until one satisfies the test; it returns all the output text seen.
func (s *stream) until(t *testing.T, want func(kind, data string) bool) string {
	t.Helper()
	var out strings.Builder
	timeout := time.After(10 * time.Second)
	for {
		select {
		case e, ok := <-s.events:
			if !ok {
				t.Fatalf("the stream ended; output so far %q", out.String())
			}
			if e[0] == "out" {
				var ev struct{ Text string }
				_ = json.Unmarshal([]byte(e[1]), &ev)
				out.WriteString(ev.Text)
			}
			if want(e[0], e[1]) {
				return out.String()
			}
		case <-timeout:
			t.Fatalf("timeout; output so far %q", out.String())
		}
	}
}

func state(name string) func(string, string) bool {
	return func(k, d string) bool { return k == "state" && strings.Contains(d, `"state":"`+name+`"`) }
}

func TestARunShowsItsOutputAndItsExitStatus(t *testing.T) {
	st := live(t, false)
	id, code := st.start(t, `{"action":"preflight","machine":"web"}`)
	if code != 200 || id == "" {
		t.Fatal(code)
	}
	p := st.spawn.next(t)
	if strings.Join(p.argv, " ") != "preflight web" {
		t.Errorf("argv %v", p.argv)
	}
	s := st.attach(t, id)
	p.say("ok  settings\nthe token is s3cr3t-value-123\n")
	p.end(1)
	out := s.until(t, func(k, d string) bool { return state("done")(k, d) && strings.Contains(d, `"code":1`) })
	if !strings.Contains(out, "$ viper preflight web") || !strings.Contains(out, "ok  settings") || strings.Contains(out, "s3cr3t") || !strings.Contains(out, "[hidden]") {
		t.Errorf("output %q", out)
	}
}

func TestAQuestionWithoutALineEndIsShownAndAnswered(t *testing.T) {
	st := live(t, false)
	id, _ := st.start(t, `{"action":"down","machine":"web"}`)
	p := st.spawn.next(t)
	s := st.attach(t, id)
	p.say("Delete web? Type DELETE to continue: ")
	out := s.until(t, state("waiting"))
	if !strings.HasSuffix(out, "Type DELETE to continue: ") {
		t.Errorf("output %q", out)
	}
	got := make(chan string, 1)
	go func() { l, _ := bufio.NewReader(p.inR).ReadString('\n'); got <- l }()
	if code, b := st.post(t, "/api/answer", `{"id":"`+id+`","text":"DELETE"}`); code != 200 {
		t.Fatal(code, b)
	}
	if l := <-got; l != "DELETE\n" {
		t.Errorf("the process got %q", l)
	}
	out += s.until(t, state("running"))
	p.say("deleted\n")
	p.end(0)
	out += s.until(t, state("done"))
	if !strings.Contains(out, "DELETE\n") || !strings.Contains(out, "deleted") {
		t.Errorf("output %q", out)
	}
}

func TestOnlyAQuestionTakesAnAnswer(t *testing.T) {
	st := live(t, false)
	id, _ := st.start(t, `{"action":"up","machine":"web"}`)
	p := st.spawn.next(t)
	s := st.attach(t, id)
	p.say("working\n")
	s.until(t, func(k, d string) bool { return k == "out" && strings.Contains(d, "working") })
	if code, _ := st.post(t, "/api/answer", `{"id":"`+id+`","text":"DELETE"}`); code != http.StatusConflict {
		t.Errorf("answer to nothing: %d", code)
	}
	p.say("Type DELETE: ")
	s.until(t, state("waiting"))
	for _, bad := range []string{strings.Repeat("x", 300), "a\\nb", "a\\u0000b"} {
		if code, _ := st.post(t, "/api/answer", `{"id":"`+id+`","text":"`+bad+`"}`); code != http.StatusBadRequest {
			t.Errorf("answer %q: %d", bad, code)
		}
	}
	p.end(0)
}

func TestACancelIsCtrlCAndThenKill(t *testing.T) {
	st := live(t, false)
	id, _ := st.start(t, `{"action":"up","machine":"web"}`)
	p := st.spawn.next(t)
	s := st.attach(t, id)
	p.say("working\n")
	if code, _ := st.post(t, "/api/cancel", `{"id":"`+id+`"}`); code != 200 {
		t.Fatal(code)
	}
	s.until(t, state("cancelling"))
	select {
	case <-p.interrupts:
	case <-time.After(5 * time.Second):
		t.Fatal("no interrupt")
	}
	p.say("interrupted\n")
	p.end(130)
	s.until(t, func(k, d string) bool { return state("done")(k, d) && strings.Contains(d, `"code":130`) })
}

func TestOneRunPerMachineAndAGroupHoldsItsMembers(t *testing.T) {
	st := live(t, false)
	_, code := st.start(t, `{"action":"up","machine":"web"}`)
	p := st.spawn.next(t)
	if code != 200 {
		t.Fatal(code)
	}
	if _, code := st.start(t, `{"action":"down","machine":"web"}`); code != http.StatusConflict {
		t.Errorf("a second run of web: %d", code)
	}
	gid, code := st.start(t, `{"action":"up","scope":"group","group":"dev"}`)
	if code != 200 {
		t.Fatalf("group run: %d", code)
	}
	g := st.spawn.next(t)
	if strings.Join(g.argv, " ") != "up --group dev --no-code" {
		t.Errorf("argv %v", g.argv)
	}
	if _, code := st.start(t, `{"action":"down","machine":"dev1","group":"dev"}`); code != http.StatusConflict {
		t.Errorf("a member of a running group: %d", code)
	}
	p.end(0)
	g.end(0)
	s := st.attach(t, gid)
	s.until(t, state("done"))
	// the machines are free again
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, code := st.start(t, `{"action":"down","machine":"web"}`); code == 200 {
			st.spawn.next(t).end(0)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the machine stayed held")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestOnlyKnownActionsAndNamesGetAProcess(t *testing.T) {
	st := live(t, false)
	for _, body := range []string{
		`{"action":"ssh","machine":"web"}`, `{"action":"up","machine":"nope"}`, `{"action":"up","machine":"--dir"}`, `{"action":"up","machine":"bad"}`,
		`{"action":"up","machine":"web","group":"dev"}`, `{"action":"up","scope":"group","group":"nope"}`, `{"action":"down","machine":"web","repo":"https://x.example/r.git"}`,
		`{"action":"up","machine":"web","repo":"--evil"}`, `{"action":"up","machine":"web","repo":"a b"}`, `{"action":"up","machine":"web","extra":1}`, `nope`,
	} {
		if _, code := st.start(t, body); code < 400 {
			t.Errorf("%s: %d", body, code)
		}
	}
	select {
	case p := <-st.spawn.ch:
		t.Errorf("a process was started: %v", p.argv)
	default:
	}
	id, code := st.start(t, `{"action":"up","machine":"dev1","group":"dev","repo":"https://x.example/r.git"}`)
	if code != 200 {
		t.Fatal(code)
	}
	p := st.spawn.next(t)
	if strings.Join(p.argv, " ") != "up dev1 --in-group dev --no-code --repo=https://x.example/r.git" {
		t.Errorf("argv %v", p.argv)
	}
	p.end(0)
	_ = id
}

func TestReadOnlyOffersNoRun(t *testing.T) {
	st := live(t, true)
	if _, code := st.start(t, `{"action":"up","machine":"web"}`); code != http.StatusForbidden {
		t.Errorf("run: %d", code)
	}
}

func TestARunIsFoundAgainAndRunsNeedTheGuard(t *testing.T) {
	st := live(t, false)
	id, _ := st.start(t, `{"action":"preflight","machine":"web"}`)
	p := st.spawn.next(t)
	resp, err := st.c.Get(st.base + "/api/runs")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !bytes.Contains(b, []byte(id)) || !bytes.Contains(b, []byte(`"state":"running"`)) {
		t.Errorf("runs: %s", b)
	}
	// a POST from another origin starts nothing
	req, _ := http.NewRequest("POST", st.base+"/api/run", strings.NewReader(`{"action":"up","machine":"dev1","group":"dev"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Viper-UI", "1")
	req.Header.Set("Origin", "http://evil.example")
	r2, err := st.c.Do(req)
	if err != nil || r2.StatusCode != http.StatusForbidden {
		t.Errorf("foreign origin: %v %v", err, r2)
	}
	if r2 != nil {
		_ = r2.Body.Close()
	}
	p.end(0)
}

func TestTheServerEndsItsRunsWithCtrlC(t *testing.T) {
	f := newFake()
	sp := &spawns{ch: make(chan *proc, 2)}
	s := web.New(web.Config{Backend: f, Spawn: func(argv []string) (web.Proc, error) { return sp.spawn(argv) }})
	ln, _ := s.Listen(0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Serve(ctx, ln); close(done) }()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if r, err := c.Get(s.URL()); err != nil {
		t.Fatal(err)
	} else {
		_ = r.Body.Close()
	}
	base := "http://" + s.Host()
	req, _ := http.NewRequest("POST", base+"/api/run", strings.NewReader(`{"action":"up","machine":"web"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Viper-UI", "1")
	req.Header.Set("Origin", base)
	resp, err := c.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatal(err, resp)
	}
	_ = resp.Body.Close()
	p := <-sp.ch
	cancel()
	select {
	case <-p.interrupts:
	case <-time.After(10 * time.Second):
		t.Fatal("the run was not interrupted")
	}
	p.end(130)
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the server did not end")
	}
}
