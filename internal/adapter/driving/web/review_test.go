package web_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/web"
)

// A message that is refused takes no file with it: 8 refused sends do not fill the table of files.
func TestARefusedRunTakesNoFileWithIt(t *testing.T) {
	st := live(t, false)
	for i := 0; i < 10; i++ {
		code, body := st.upload(t, "kind=pdf&name=r.pdf", "x", "application/octet-stream", st.base)
		if code != 200 {
			t.Fatalf("upload %d: %d %s", i, code, body)
		}
		up := uploadID(t, body)
		if _, c := st.start(t, `{"action":"nats","sub":"broadcast","nats":"dev","topic":"-bad","text":"x","upload":"`+up+`"}`); c != http.StatusBadRequest {
			t.Fatalf("run %d: %d", i, c)
		}
	}
}

func TestAFileThatNobodySendsGoesAway(t *testing.T) {
	st := liveWith(t, false, func(c *web.Config) { c.UploadLife = 150 * time.Millisecond })
	_, body := st.upload(t, "kind=pdf", "x", "application/octet-stream", st.base)
	up := uploadID(t, body)
	time.Sleep(500 * time.Millisecond)
	if _, c := st.start(t, `{"action":"nats","sub":"send","nats":"dev","to":"dev1","upload":"`+up+`"}`); c != http.StatusBadRequest {
		t.Errorf("an expired file: %d", c)
	}
}

func TestTheLimitOfTheGroupAppliesToAFile(t *testing.T) {
	st := live(t, false)
	if code, _ := st.upload(t, "kind=pdf&group=bare", "x", "application/octet-stream", st.base); code != http.StatusConflict {
		t.Errorf("a group without an object store: %d", code)
	}
	if code, _ := st.upload(t, "kind=pdf&group=nope", "x", "application/octet-stream", st.base); code != http.StatusBadRequest {
		t.Errorf("no such group: %d", code)
	}
	if code, b := st.upload(t, "kind=pdf&group=dev", strings.Repeat("x", 8<<20+1), "application/octet-stream", st.base); code != http.StatusRequestEntityTooLarge || !strings.Contains(b, "8 MiB") {
		t.Errorf("a file over the limit of the group: %d %s", code, b)
	}
}

// Many uploads at once cannot get past the limit of 8 files.
func TestUploadsAtOnceCannotPassTheLimit(t *testing.T) {
	st := live(t, false)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if code, _ := st.upload(t, "kind=zip", strings.Repeat("x", 1000), "application/octet-stream", st.base); code == 200 {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 8 {
		t.Errorf("%d uploads were taken, 8 is the limit", ok)
	}
}

// Open streams have a budget of their own: with all of it used, a cancel still gets through.
func TestOpenStreamsDoNotStarveCancel(t *testing.T) {
	st := live(t, false)
	id, _ := st.start(t, `{"action":"up","machine":"web"}`)
	p := st.spawn.next(t)
	var bodies []io.Closer
	for i := 0; i < 8; i++ {
		resp, err := st.c.Get(st.base + "/api/events?id=" + id) //nolint:bodyclose // closed below
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("stream %d: %v %v", i, err, resp)
		}
		bodies = append(bodies, resp.Body)
	}
	defer func() {
		for _, b := range bodies {
			_ = b.Close()
		}
	}()
	if resp, err := st.c.Get(st.base + "/api/events?id=" + id); err != nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("a ninth stream: %v %v", err, resp)
	} else {
		_ = resp.Body.Close()
	}
	if code, _ := st.post(t, "/api/cancel", `{"id":"`+id+`"}`); code != 200 {
		t.Errorf("cancel with the streams open: %d", code)
	}
	if resp, err := st.c.Get(st.base + "/api/overview"); err != nil || resp.StatusCode != 200 {
		t.Errorf("overview with the streams open: %v %v", err, resp)
	} else {
		_ = resp.Body.Close()
	}
	select {
	case <-p.interrupts:
	case <-time.After(5 * time.Second):
		t.Fatal("no interrupt")
	}
	p.end(130)
}

// The first line of a run is shown as the rest is: cleaned, and with the secrets out. A repository with credentials is refused.
func TestTheHeaderOfARunIsRedactedAndACredentialInTheRepositoryIsRefused(t *testing.T) {
	st := live(t, false)
	if _, c := st.start(t, `{"action":"up","machine":"web","repo":"https://u:pw@example.org/r.git"}`); c != http.StatusBadRequest {
		t.Errorf("credentials in the url: %d", c)
	}
	if _, c := st.start(t, "{\"action\":\"up\",\"machine\":\"web\",\"repo\":\"https://example.org/r\\u0085.git\"}"); c != http.StatusBadRequest {
		t.Errorf("a control character: %d", c)
	}
	id, c := st.start(t, `{"action":"up","machine":"web","repo":"https://example.org/s3cr3t-value-123.git"}`)
	if c != 200 {
		t.Fatal(c)
	}
	p := st.spawn.next(t)
	s := st.attach(t, id)
	p.end(0)
	out := s.until(t, state("done"))
	if strings.Contains(out, "s3cr3t") || !strings.Contains(out, "[hidden]") {
		t.Errorf("header %q", out)
	}
}

// A secret that comes in two pieces, with a silence between them, is still taken out.
func TestASecretInTwoPiecesIsStillRedacted(t *testing.T) {
	st := live(t, false)
	id, _ := st.start(t, `{"action":"up","machine":"web"}`)
	p := st.spawn.next(t)
	s := st.attach(t, id)
	p.say("the key is s3cr3t-val")
	time.Sleep(900 * time.Millisecond) // more than the silence after which an open line is shown
	p.say("ue-123 and more\n")
	p.end(0)
	out := s.until(t, state("done"))
	if strings.Contains(out, "s3cr3t") || !strings.Contains(out, "[hidden] and more") {
		t.Errorf("output %q", out)
	}
}

// A question still comes at once, also one that ends in a long word.
func TestAQuestionIsShownAtOnce(t *testing.T) {
	st := live(t, false)
	id, _ := st.start(t, `{"action":"down","machine":"web"}`)
	p := st.spawn.next(t)
	s := st.attach(t, id)
	p.say("Type DELETE to continue: ")
	s.until(t, state("waiting"))
	p.end(1)
	s.until(t, state("done"))
}

func TestOnlyOneOfTwoAnswersGetsTheQuestion(t *testing.T) {
	st := live(t, false)
	id, _ := st.start(t, `{"action":"down","machine":"web"}`)
	p := st.spawn.next(t)
	s := st.attach(t, id)
	lines := make(chan string, 4)
	go func() {
		r := io.Reader(p.inR)
		buf := make([]byte, 64)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				lines <- string(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	p.say("Type DELETE: ")
	s.until(t, state("waiting"))
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, ans := range []string{"DELETE", "DELETE"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, _ := st.post(t, "/api/answer", `{"id":"`+id+`","text":"`+ans+`"}`)
			codes <- c
		}()
	}
	wg.Wait()
	close(codes)
	ok, conflict := 0, 0
	for c := range codes {
		switch c {
		case 200:
			ok++
		case http.StatusConflict:
			conflict++
		}
	}
	if ok != 1 || conflict != 1 {
		t.Errorf("answers: %d accepted, %d refused", ok, conflict)
	}
	if got := <-lines; got != "DELETE\n" {
		t.Errorf("the process got %q", got)
	}
	select {
	case more := <-lines:
		t.Errorf("a second line reached the process: %q", more)
	case <-time.After(300 * time.Millisecond):
	}
	p.end(0)
}

// A stream that is joined again goes on after the last event it got, and one that missed much is told.
func TestAStreamGoesOnAfterTheLastEvent(t *testing.T) {
	st := live(t, false)
	id, _ := st.start(t, `{"action":"up","machine":"web"}`)
	p := st.spawn.next(t)
	first := st.attach(t, id)
	p.say("one\n")
	first.until(t, func(k, d string) bool { return k == "out" && strings.Contains(d, "one") })
	p.say("two\n")
	first.until(t, func(k, d string) bool { return k == "out" && strings.Contains(d, "two") })
	first.cancel()
	p.say("three\n")
	p.end(0)
	req, _ := http.NewRequest("GET", st.base+"/api/events?id="+id, nil)
	req.Header.Set("Last-Event-ID", "2") // 0: the header, 1: one, 2: two
	resp, err := st.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	got := string(b)
	if strings.Contains(got, `"text":"one`) || strings.Contains(got, `"text":"two`) || !strings.Contains(got, `"text":"three`) {
		t.Errorf("stream after Last-Event-ID: %s", got)
	}
	if !strings.Contains(got, "id: 3\n") {
		t.Errorf("events have an id: %s", got)
	}
}

func TestAStreamSaysThatOldOutputWasCut(t *testing.T) {
	st := live(t, false)
	id, _ := st.start(t, `{"action":"up","machine":"web"}`)
	p := st.spawn.next(t)
	line := strings.Repeat("x", 1000) + "\n"
	for i := 0; i < 1300; i++ { // more than the 1 MiB that is kept
		p.say(line)
	}
	p.end(0)
	st.attach(t, id).until(t, state("done")) // wait for the end, then join late
	resp, err := st.c.Get(st.base + "/api/events?id=" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(b, []byte("earlier output was cut")) {
		t.Errorf("a late stream is not told: %.200s", b)
	}
}

// A cancel that comes before the process is there is carried out when it is.
func TestACancelBeforeTheProcessIsThereIsNotLost(t *testing.T) {
	f := newFake()
	release := make(chan struct{})
	made := make(chan *proc, 1)
	cfg := web.Config{Backend: f, Spawn: func(argv []string) (web.Proc, error) {
		<-release
		p := newProc(argv)
		made <- p
		return p, nil
	}}
	st := serveWith(t, cfg)
	go func() { _, _ = st.start(t, `{"action":"up","machine":"web"}`) }()
	var id string
	deadline := time.Now().Add(5 * time.Second)
	for id == "" && time.Now().Before(deadline) {
		resp, err := st.c.Get(st.base + "/api/runs")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if i := strings.Index(string(b), `"id":"`); i >= 0 {
			id = string(b)[i+6 : i+6+32]
		}
		time.Sleep(10 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("the run is not in the table")
	}
	if code, _ := st.post(t, "/api/cancel", `{"id":"`+id+`"}`); code != 200 {
		t.Fatal(code)
	}
	close(release)
	p := <-made
	select {
	case <-p.interrupts:
	case <-time.After(5 * time.Second):
		t.Fatal("the cancel was lost")
	}
	p.end(130)
}

// The runs get their Ctrl-C at once when the server ends, also while a long request is still being answered.
func TestRunsAreInterruptedWhileALongRequestHolds(t *testing.T) {
	f := newFake()
	f.checks, f.entered = make(chan struct{}), make(chan struct{}, 1)
	sp := &spawns{ch: make(chan *proc, 2)}
	s := web.New(web.Config{Backend: f, Spawn: func(argv []string) (web.Proc, error) { return sp.spawn(argv) }})
	ln, _ := s.Listen(0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Serve(ctx, ln); close(done) }()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if r, err := c.Get(s.URL()); err == nil {
		_ = r.Body.Close()
	}
	st := &site{fake: f, s: s, c: c, base: "http://" + s.Host(), spawn: sp}
	if _, code := st.start(t, `{"action":"up","machine":"web"}`); code != 200 {
		t.Fatal(code)
	}
	p := <-sp.ch
	go func() { _, _ = st.post(t, "/api/check", `{"machine":"dev1","group":"dev"}`) }() // holds until f.checks is closed
	<-f.entered
	start := time.Now()
	cancel()
	select {
	case <-p.interrupts:
	case <-time.After(5 * time.Second):
		t.Fatal("the run was not interrupted while the check held the server")
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("it took %v", time.Since(start))
	}
	close(f.checks)
	p.end(130)
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the server did not end")
	}
}

func TestServeCleansUpWhenTheListenerFails(t *testing.T) {
	s := web.New(web.Config{Backend: newFake()})
	ln, err := s.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), ln) }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a failed listener is an error")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Serve did not return")
	}
}

// The overview is read once for many requests, and anew after a run has ended.
func TestTheOverviewIsNotReadAgainForEveryRequest(t *testing.T) {
	st := live(t, false)
	before := st.fake.calls.Load()
	for i := 0; i < 10; i++ {
		resp, err := st.c.Get(st.base + "/api/overview")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	if n := st.fake.calls.Load() - before; n > 1 {
		t.Errorf("the overview was read %d times for 10 requests", n)
	}
	_, _ = st.start(t, `{"action":"up","machine":"web"}`)
	st.spawn.next(t).end(0)
	deadline := time.Now().Add(5 * time.Second)
	for {
		n := st.fake.calls.Load()
		resp, err := st.c.Get(st.base + "/api/overview")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if st.fake.calls.Load() > n {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the overview was not read again after the run")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// NATS runs have a budget of their own: four listeners do not stop a preflight, and a fifth listener is refused.
func TestNATSRunsHaveTheirOwnBudgetAndTheListIsLabelled(t *testing.T) {
	st := live(t, false)
	var procs []*proc
	defer func() {
		for _, p := range procs {
			p.end(130) // the server waits for its runs when it ends
		}
	}()
	for i := 0; i < 4; i++ {
		if _, c := st.start(t, `{"action":"nats","sub":"listen","nats":"dev"}`); c != 200 {
			t.Fatalf("listener %d: %d", i, c)
		}
		procs = append(procs, st.spawn.next(t))
	}
	if _, c := st.start(t, `{"action":"nats","sub":"listen","nats":"dev"}`); c != http.StatusServiceUnavailable {
		t.Errorf("a fifth listener: %d", c)
	}
	if _, c := st.start(t, `{"action":"preflight","machine":"web"}`); c != 200 {
		t.Errorf("a preflight next to four listeners: %d", c)
	} else {
		procs = append(procs, st.spawn.next(t))
	}
	resp, _ := st.c.Get(st.base + "/api/runs")
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(b), `"sub":"listen"`) || !strings.Contains(string(b), `"nats":"dev"`) {
		t.Errorf("runs: %s", b)
	}
}

func serveWith(t *testing.T, cfg web.Config) *site {
	t.Helper()
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
	r, err := c.Get(s.URL())
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Error("the server did not end")
		}
	})
	return &site{s: s, c: c, base: "http://" + s.Host()}
}

// A run that is being started when the server ends is cancelled as well, and a start that fails then leaves nobody waiting.
func TestARunThatIsBeingStartedWhenTheServerEndsIsCancelled(t *testing.T) {
	f := newFake()
	release := make(chan struct{})
	made := make(chan *proc, 1)
	s := web.New(web.Config{Backend: f, Spawn: func(argv []string) (web.Proc, error) {
		<-release
		p := newProc(argv)
		made <- p
		return p, nil
	}})
	ln, _ := s.Listen(0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Serve(ctx, ln); close(done) }()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if r, err := c.Get(s.URL()); err == nil {
		_ = r.Body.Close()
	}
	st := &site{fake: f, s: s, c: c, base: "http://" + s.Host()}
	go func() { _, _ = st.start(t, `{"action":"up","machine":"web"}`) }()
	waitForRun(t, st)
	cancel() // the server ends while the process is still being made
	time.Sleep(300 * time.Millisecond)
	close(release)
	p := <-made
	select {
	case <-p.interrupts:
	case <-time.After(10 * time.Second):
		t.Fatal("a run that was started during the end was left alone")
	}
	p.end(130)
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the server did not end")
	}
}

func TestAFailedStartDuringTheEndLeavesNobodyWaiting(t *testing.T) {
	f := newFake()
	release := make(chan struct{})
	s := web.New(web.Config{Backend: f, Spawn: func([]string) (web.Proc, error) {
		<-release
		return nil, io.ErrClosedPipe
	}})
	ln, _ := s.Listen(0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Serve(ctx, ln); close(done) }()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if r, err := c.Get(s.URL()); err == nil {
		_ = r.Body.Close()
	}
	st := &site{fake: f, s: s, c: c, base: "http://" + s.Host()}
	go func() { _, _ = st.start(t, `{"action":"up","machine":"web"}`) }()
	waitForRun(t, st)
	cancel()
	time.Sleep(200 * time.Millisecond)
	close(release)
	select {
	case <-done:
	case <-time.After(10 * time.Second): // not the 20 seconds of the wait for a run that is gone
		t.Fatal("the server waited for a run that never began")
	}
}

// waitForRun waits until a run is in the table (its process is still being made).
func waitForRun(t *testing.T, st *site) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := st.c.Get(st.base + "/api/runs")
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if strings.Contains(string(b), `"id":"`) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the run is not in the table")
}

// A secret that is longer than the tail that an open line keeps is not cut in two by the cut of a line that has no blank.
func TestALongSecretInALineWithoutBlanksIsNotCutInTwo(t *testing.T) {
	long := strings.Repeat("Q", 300)
	cfg := web.Config{Backend: newFake(), Redact: func(s string) string { return strings.ReplaceAll(s, long, "[hidden]") }, LongestSecret: func() int { return 300 }}
	sp := &spawns{ch: make(chan *proc, 2)}
	cfg.Spawn = func(argv []string) (web.Proc, error) { return sp.spawn(argv) }
	st := serveWith(t, cfg)
	st.spawn = sp
	id, c := st.start(t, `{"action":"up","machine":"web"}`)
	if c != 200 {
		t.Fatal(c)
	}
	p := sp.next(t)
	s := st.attach(t, id)
	p.say(strings.Repeat("x", 3900) + long[:200]) // longer than a page, no blank: it is cut, but not inside the secret
	time.Sleep(100 * time.Millisecond)
	p.say(long[200:] + "\n")
	p.end(0)
	out := s.until(t, state("done"))
	if strings.Contains(out, "QQQQ") || !strings.Contains(out, "[hidden]") {
		t.Errorf("part of the secret is in the output (%d bytes)", len(out))
	}
}

// An upload that stalls does not hold its place for ever.
func TestAStalledUploadGivesItsPlaceBack(t *testing.T) {
	st := liveWith(t, false, func(c *web.Config) { c.UploadLife = 200 * time.Millisecond })
	var pipes []*io.PipeWriter
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		pr, pw := io.Pipe()
		pipes = append(pipes, pw)
		req, _ := http.NewRequest("POST", st.base+"/api/upload?kind=zip&group=dev", pr)
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("X-Viper-UI", "1")
		req.Header.Set("Origin", st.base)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if resp, err := st.c.Do(req); err == nil {
				_ = resp.Body.Close()
			}
		}()
		_, _ = pw.Write([]byte("x")) // the body has begun and does not go on
	}
	time.Sleep(600 * time.Millisecond)
	if code, b := st.upload(t, "kind=zip", "x", "application/octet-stream", st.base); code != 200 {
		t.Errorf("a ninth upload after the stalled ones timed out: %d %s", code, b)
	}
	for _, pw := range pipes {
		_ = pw.Close()
	}
	wg.Wait()
}

// What lets a command through and what the output must not show is read anew, also within a second.
func TestChecksReadTheOverviewAnewButThePageMayUseTheCached(t *testing.T) {
	st := live(t, false)
	before := st.fake.calls.Load()
	for i := 0; i < 3; i++ {
		resp, err := st.c.Get(st.base + "/api/log?machine=web")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	if n := st.fake.calls.Load() - before; n < 3 {
		t.Errorf("3 log requests read the overview %d times", n)
	}
}

func TestTwoServersDoNotShareACookieName(t *testing.T) {
	a, b := web.New(web.Config{Backend: newFake()}), web.New(web.Config{Backend: newFake()})
	for _, s := range []*web.Server{a, b} {
		ln, err := s.Listen(0)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = ln.Close() }()
	}
	if a.CookieName() == b.CookieName() || !strings.HasPrefix(a.CookieName(), "viper_ui_") {
		t.Errorf("%q %q", a.CookieName(), b.CookieName())
	}
}
