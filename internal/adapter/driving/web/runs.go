package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Proc is a running viper command: the output of the process (stdout and stderr together), its standard input, and the
// ways to end it. The cli package starts the real one; the tests have others.
type Proc interface {
	Output() io.Reader
	Input() io.Writer
	// Interrupt is Ctrl-C for the process (not for its children: viper ends those itself); Kill ends it at once.
	Interrupt() error
	Kill() error
	// Wait returns the exit status when the process has ended (130: ended by a signal).
	Wait() int
}

// Spawner starts viper with the arguments (the words after the program name).
type Spawner func(argv []string) (Proc, error)

const (
	flushAfter     = 600 * time.Millisecond // a line that is not finished after this long is shown: it is a question
	killAfter      = 20 * time.Second       // Ctrl-C is a request; after this the process is killed
	maxRuns        = 4                      // running at the same time, of the actions; the NATS runs have as many of their own
	minWordHeld    = 8                      // the last word of an unfinished line is kept back (see splitWord) when it is this long
	keepFinished   = 20
	maxRetained    = 1 << 20 // what of the output of a run is kept for a page that attaches late
	maxAnswer      = 256
	maxPendingLine = 4096
	minKeepTail    = 128 // of a line without a blank that is cut (at least; a longer secret makes it longer): a secret is not cut in two
)

// The states of a run.
const (
	stateRunning    = "running"
	stateWaiting    = "waiting" // the output ends in a question
	stateCancelling = "cancelling"
	stateDone       = "done"
)

type event struct {
	Seq  int    `json:"seq"`
	Text string `json:"text"`
}

type run struct {
	ID      string   `json:"id"`
	Action  string   `json:"action"`
	Machine string   `json:"machine,omitempty"`
	Group   string   `json:"group,omitempty"`
	Sub     string   `json:"sub,omitempty"`  // a NATS run: the subcommand
	NATS    string   `json:"nats,omitempty"` // and the group
	Names   []string `json:"-"`              // the machines the run holds

	mu            sync.Mutex
	proc          Proc // set under mu, once, after the run is in the table
	pendingCancel bool // a cancel that came before the process was there
	events        []event
	size          int
	next          int
	state         string
	code          int
	started       time.Time
	notify        chan struct{} // closed and replaced at every change
	kill          *time.Timer
	files         *runFiles // what the run has on this computer: the text, a file to send, the folder of a fetch
}

// view is what a page needs: the events from the cursor on, the state, and a channel that closes at the next change.
func (r *run) view(cursor int) (evs []event, cut bool, state string, code int, ch <-chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n := len(r.events); n > 0 {
		// the numbers are in a row (the front is trimmed, nothing else), so the place of an event is its number minus the first
		start := max(cursor-r.events[0].Seq, 0)
		if start < n {
			evs = slices.Clone(r.events[start:])
		}
	}
	// events that were dropped to keep the memory small: the page is told
	cut = len(evs) > 0 && evs[0].Seq > cursor
	return evs, cut, r.state, r.code, r.notify
}

// setProc gives the run its process (once); a cancel that came first is carried out now.
func (r *run) setProc(p Proc) {
	r.mu.Lock()
	r.proc = p
	pending := r.pendingCancel
	if pending && r.kill == nil {
		r.kill = time.AfterFunc(killAfter, func() { _ = p.Kill() })
	}
	r.mu.Unlock()
	if pending {
		_ = p.Interrupt()
	}
}

func (r *run) process() Proc { //nolint:ireturn // the process of the run is what the cli gave: any Proc
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.proc
}

// claimQuestion takes the question the run asks (waiting -> running); only the first of two answers gets it.
func (r *run) claimQuestion() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != stateWaiting {
		return false
	}
	r.state = stateRunning
	r.changed()
	return true
}

func (r *run) changed() {
	close(r.notify)
	r.notify = make(chan struct{})
}

func (r *run) add(text string) {
	if text == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event{Seq: r.next, Text: text})
	r.next++
	r.size += len(text)
	for r.size > maxRetained && len(r.events) > 1 {
		r.size -= len(r.events[0].Text)
		r.events = r.events[1:]
	}
	r.changed()
}

func (r *run) setState(state string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == stateDone || r.state == state || r.state == stateCancelling && state != stateDone {
		return
	}
	r.state = state
	r.changed()
}

func (r *run) exit() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.code
}

func (r *run) current() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

// info is the row of the list of runs.
type info struct {
	ID      string `json:"id"`
	Action  string `json:"action"`
	Machine string `json:"machine,omitempty"`
	Group   string `json:"group,omitempty"`
	Sub     string `json:"sub,omitempty"`
	NATS    string `json:"nats,omitempty"`
	State   string `json:"state"`
	Code    int    `json:"code"`
	Started string `json:"started"`
}

func (r *run) info() info {
	r.mu.Lock()
	defer r.mu.Unlock()
	return info{r.ID, r.Action, r.Machine, r.Group, r.Sub, r.NATS, r.state, r.code, r.started.Format(time.RFC3339)}
}

// Runs: a table on the server, not in a page: a page that is closed ends nothing, and a new one finds the runs again.
type runTable struct {
	mu      sync.Mutex
	closing bool // the server ends: no new run
	runs    []*run
	held    map[string]string // machine -> the run that has it
}

type runRequest struct {
	Action  string `json:"action"`
	Machine string `json:"machine"`
	Group   string `json:"group"`
	Scope   string `json:"scope"` // "machine" (default) or "group": the whole group
	Repo    string `json:"repo"`
	// the actions on a NATS group (action "nats"; Group is not used: NATS is the group)
	Sub     string `json:"sub"`     // peers, topics, listen, send, broadcast, request, fetch
	NATS    string `json:"nats"`    // the group
	To      string `json:"to"`      // a machine of the group
	From    string `json:"from"`    // the machine that speaks (default: the operator, or one of the group)
	Topic   string `json:"topic"`   // topics, with commas
	Text    string `json:"text"`    // the message
	Timeout int    `json:"timeout"` // seconds
	Query   bool   `json:"query"`
	All     bool   `json:"all"`
	Yes     bool   `json:"yes"`
	Upload  string `json:"upload"` // the id of a file that was handed over (POST /api/upload)
	ID      string `json:"id"`     // fetch: the id of the file
	SHA256  string `json:"sha256"`
}

func isAction(a string) bool { return a == "preflight" || a == "up" || a == "down" || a == "nats" }

// argv is the command line of the request, or the reason there is none. Names come from the files (the overview), so none
// starts with a dash; the repository goes in the form --repo=URL for the same reason.
func runArgv(req runRequest, ov Overview) (argv, names []string, err error) {
	if !isAction(req.Action) {
		return nil, nil, fmt.Errorf("no such action")
	}
	if !repoUsable(req) {
		return nil, nil, fmt.Errorf("the repository is not usable here")
	}
	argv = []string{req.Action}
	if req.Scope == "group" {
		g, ok := findGroup(ov, req.Group)
		if !ok || len(g.Machines) == 0 {
			return nil, nil, fmt.Errorf("no such group")
		}
		argv = append(argv, "--group", g.Name)
		names = g.Machines
	} else {
		m, ok := findMachine(ov, req.Machine, req.Group)
		if !ok {
			return nil, nil, fmt.Errorf("no such machine")
		}
		argv = append(argv, m.Name)
		if m.Group != "" {
			argv = append(argv, "--in-group", m.Group)
		}
		names = []string{m.Name}
	}
	if req.Action == "up" {
		argv = append(argv, "--no-code") // there is no desktop to open VS Code on
	}
	if req.Repo != "" {
		argv = append(argv, "--repo="+req.Repo)
	}
	return argv, names, nil
}

// repoUsable: a repository belongs to up and preflight, and is one word without control characters that is no option and has no
// credentials in it (the preflight refuses those too; a token must not reach a protocol).
func repoUsable(req runRequest) bool {
	r := req.Repo
	if r == "" {
		return true
	}
	if req.Action == "down" || len(r) > 512 || strings.HasPrefix(r, "-") || strings.ContainsFunc(r, func(c rune) bool { return unicode.IsControl(c) || unicode.IsSpace(c) }) {
		return false
	}
	if i := strings.Index(r, "://"); i >= 0 {
		rest := r[i+3:]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			rest = rest[:j]
		}
		return !strings.Contains(rest, "@")
	}
	return true
}

func findGroup(ov Overview, name string) (Group, bool) {
	for _, g := range ov.Groups {
		if g.Name == name && g.Error == "" {
			return g, true
		}
	}
	return Group{}, false
}

func findMachine(ov Overview, name, group string) (Machine, bool) {
	for _, m := range ov.Machines {
		if m.Name == name && m.Group == group && m.Error == "" {
			return m, true
		}
	}
	return Machine{}, false
}

func (s *Server) startRun(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Readonly || s.cfg.Spawn == nil {
		fail(w, http.StatusForbidden, "this page is read only")
		return
	}
	var req runRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRunBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "bad request")
		return
	}
	ov, err := s.fresh(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, s.show(err.Error()))
		return
	}
	started := false
	defer func() {
		if !started && req.Upload != "" {
			s.uploads.discardReady(req.Upload) // a run that was refused takes no file with it
		}
	}()
	var argv, names []string
	var files *runFiles
	if req.Action == "nats" {
		argv, files, err = s.natsArgv(req, ov)
	} else {
		argv, names, err = runArgv(req, ov)
	}
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	run := &run{files: files, ID: random(), Action: req.Action, Names: names, state: stateRunning, started: s.cfg.Now(), notify: make(chan struct{})}
	if req.Action == "nats" {
		run.Sub, run.NATS = req.Sub, req.NATS // checked by natsArgv: names of the overview and fixed words
	} else if req.Scope == "group" {
		run.Group = req.Group
	} else {
		run.Machine, run.Group = req.Machine, req.Group // found in the overview
	}
	if code, msg := s.hold(run); code != 0 {
		files.remove()
		fail(w, code, msg)
		return
	}
	proc, err := s.cfg.Spawn(argv)
	if err != nil {
		s.release(run)
		files.remove()
		fail(w, http.StatusInternalServerError, s.show(err.Error()))
		return
	}
	started = true
	run.add(s.show("$ viper " + strings.Join(argv, " ") + "\n"))
	run.setProc(proc)
	go s.pump(run)
	reply(w, map[string]string{"id": run.ID})
}

// hold puts the run in the table if the machines are free: one run per machine, and a limit on the runs at once.
func (s *Server) hold(r *run) (int, string) {
	t := &s.runs
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closing {
		return http.StatusServiceUnavailable, "viper ui is ending"
	}
	active := 0
	for _, o := range t.runs {
		if o.current() != stateDone && (o.Action == "nats") == (r.Action == "nats") {
			active++
		}
	}
	if active >= maxRuns {
		return http.StatusServiceUnavailable, "too many runs at once"
	}
	for _, n := range r.Names {
		if id, busy := t.held[n]; busy {
			return http.StatusConflict, "a run holds the machine " + n + " (" + id[:6] + ")"
		}
	}
	if t.held == nil {
		t.held = map[string]string{}
	}
	for _, n := range r.Names {
		t.held[n] = r.ID
	}
	t.runs = append(t.runs, r)
	t.forgetFinished()
	return 0, ""
}

// forgetFinished drops the oldest finished runs beyond what is kept; the caller holds the lock.
func (t *runTable) forgetFinished() {
	for {
		finished := 0
		for _, o := range t.runs {
			if o.current() == stateDone {
				finished++
			}
		}
		if finished <= keepFinished {
			return
		}
		for i, o := range t.runs {
			if o.current() == stateDone {
				o.files.remove()
				t.runs = append(t.runs[:i], t.runs[i+1:]...)
				break
			}
		}
	}
}

func (s *Server) release(r *run) {
	t := &s.runs
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, n := range r.Names {
		if t.held[n] == r.ID {
			delete(t.held, n)
		}
	}
	for i, o := range t.runs {
		if o == r && r.process() == nil { // never started: it is gone, and nobody waits for it
			t.runs = append(t.runs[:i], t.runs[i+1:]...)
			r.mu.Lock()
			r.state = stateDone
			r.changed()
			r.mu.Unlock()
			break
		}
	}
}

// close lets no new run in.
func (t *runTable) close() {
	t.mu.Lock()
	t.closing = true
	t.mu.Unlock()
}

func (s *Server) lookupRun(id string) *run {
	s.runs.mu.Lock()
	defer s.runs.mu.Unlock()
	for _, r := range s.runs.runs {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// pump shows the output as it comes: whole lines at once, and a line that stays open (a question) after a short silence. A long
// word at the end of such a line is kept back for one more silence first: it may be the first half of a token that a
// redaction would not find in two pieces.
func (s *Server) pump(r *run) {
	proc := r.process()
	chunks := readChunks(proc.Output())
	var pending []byte
	held := false // the last word of pending was kept back once already
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	for chunks != nil {
		select {
		case c, ok := <-chunks:
			if !ok {
				chunks = nil
				break
			}
			pending, held = s.lines(r, append(pending, c...)), false
			if len(pending) > 0 {
				timer.Reset(flushAfter)
			} else {
				timer.Stop()
			}
			r.setState(stateRunning)
		case <-timer.C:
			pending, held = s.flushOpenLine(r, pending, held)
			if len(pending) > 0 {
				timer.Reset(flushAfter)
			}
		}
	}
	if len(pending) > 0 {
		r.add(s.show(string(pending)))
	}
	s.finish(r, proc.Wait())
}

// flushOpenLine shows an unfinished line after a silence: the question. Its last long word waits for one more silence.
func (s *Server) flushOpenLine(r *run, pending []byte, held bool) (rest []byte, nowHeld bool) {
	if len(pending) == 0 {
		return nil, false
	}
	emit, keep := pending, []byte(nil)
	if !held {
		emit, keep = splitWord(pending)
	}
	if len(emit) > 0 {
		r.add(s.show(string(emit)))
	}
	if len(keep) > 0 {
		return keep, true
	}
	r.setState(stateWaiting)
	return nil, false
}

// splitWord cuts the last word off a line that is not finished, if it is a long one that does not end like a question
// ("... continue: ", "Password:", "> "): there the silence is a person's turn, and the question is shown at once.
func splitWord(b []byte) (emit, keep []byte) {
	if len(b) == 0 {
		return b, nil
	}
	if last := b[len(b)-1]; last == ' ' || last == '\t' || strings.IndexByte(":?>$#]", last) >= 0 {
		return b, nil
	}
	i := bytes.LastIndexAny(b, " \t") + 1
	if len(b)-i < minWordHeld {
		return b, nil
	}
	return b[:i], b[i:]
}

// readChunks reads the output as it comes, in pieces and not in lines: a question has no line end.
func readChunks(out io.Reader) chan []byte {
	chunks := make(chan []byte)
	go func() {
		defer close(chunks)
		buf := make([]byte, 4096)
		for {
			n, err := out.Read(buf)
			if n > 0 {
				chunks <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				return
			}
		}
	}()
	return chunks
}

// lines shows what ends in a new line and returns the rest. A rest that is longer than a page of text is cut at its last white
// space (a token is not cut in two), or, in a line without any, a little before its end.
func (s *Server) lines(r *run, b []byte) []byte {
	if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
		r.add(s.show(string(b[:i+1])))
		b = b[i+1:]
	}
	if len(b) <= maxPendingLine {
		return b
	}
	cut := bytes.LastIndexAny(b, " \t") + 1
	if cut == 0 {
		// a secret that begins in the part that is shown would end in the part that is kept: it is not longer than the tail
		cut = max(len(b)-max(minKeepTail, s.longestSecret()), len(b)/2)
	}
	for cut > 0 && cut < len(b) && !utf8.RuneStart(b[cut]) { // not in the middle of a character
		cut--
	}
	r.add(s.show(string(b[:cut])))
	return b[cut:]
}

func (s *Server) longestSecret() int {
	if s.cfg.LongestSecret == nil {
		return minKeepTail
	}
	return s.cfg.LongestSecret()
}

func (s *Server) finish(r *run, code int) {
	r.mu.Lock()
	if r.kill != nil {
		r.kill.Stop()
	}
	r.code, r.state = code, stateDone
	r.changed()
	r.mu.Unlock()
	s.release(r)
	s.forgetOverview() // what the run did (a machine made or deleted) is in the next overview
	r.files.finish()   // the text and the file that went out; a fetched file stays for the page to take
}

func (r *run) cancel() {
	r.mu.Lock()
	if r.state == stateDone || r.state == stateCancelling {
		r.mu.Unlock()
		return
	}
	r.state = stateCancelling
	p := r.proc
	if p == nil { // setProc will do it
		r.pendingCancel = true
	} else {
		r.kill = time.AfterFunc(killAfter, func() { _ = p.Kill() })
	}
	r.changed()
	r.mu.Unlock()
	if p != nil {
		_ = p.Interrupt()
	}
}

func (s *Server) listRuns(w http.ResponseWriter, _ *http.Request) {
	s.runs.mu.Lock()
	out := make([]info, 0, len(s.runs.runs))
	for _, r := range s.runs.runs {
		out = append(out, r.info())
	}
	s.runs.mu.Unlock()
	reply(w, map[string]any{"runs": out})
}

type idRequest struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

func (s *Server) runBody(w http.ResponseWriter, r *http.Request) (*run, idRequest, bool) {
	var req idRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "bad request")
		return nil, req, false
	}
	run := s.lookupRun(req.ID)
	if run == nil {
		fail(w, http.StatusNotFound, "no such run")
		return nil, req, false
	}
	return run, req, true
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	run, _, ok := s.runBody(w, r)
	if !ok {
		return
	}
	run.cancel()
	reply(w, map[string]bool{"ok": true})
}

// answer gives a line to the question the run asks, by its standard input: the person types the word (DELETE) themself.
func (s *Server) answer(w http.ResponseWriter, r *http.Request) {
	run, req, ok := s.runBody(w, r)
	if !ok {
		return
	}
	if len(req.Text) > maxAnswer || strings.ContainsFunc(req.Text, func(c rune) bool { return c < ' ' || c == 0x7f }) {
		fail(w, http.StatusBadRequest, "the answer is one short line")
		return
	}
	if !run.claimQuestion() { // the first of two answers gets it
		fail(w, http.StatusConflict, "the run asks nothing now")
		return
	}
	if _, err := io.WriteString(run.process().Input(), req.Text+"\n"); err != nil {
		fail(w, http.StatusConflict, "the run has ended")
		return
	}
	run.add(s.show(req.Text) + "\n")
	reply(w, map[string]bool{"ok": true})
}

// events streams the output of a run to a page (server-sent events). It changes nothing: a GET only attaches. Every event has
// its number as id, so a stream that the browser reconnects goes on after the last event it got (Last-Event-ID).
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	run := s.lookupRun(r.URL.Query().Get("id"))
	fl, ok := w.(http.Flusher)
	if run == nil || !ok {
		fail(w, http.StatusNotFound, "no such run")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	cursor, last := 0, ""
	if n, err := strconv.Atoi(r.Header.Get("Last-Event-ID")); err == nil && n >= 0 {
		cursor = n + 1
	}
	keep := time.NewTimer(15 * time.Second)
	defer keep.Stop()
	for {
		evs, cut, state, code, ch := run.view(cursor)
		if cut {
			fmt.Fprint(w, "event: out\ndata: {\"text\":\"[... earlier output was cut]\\n\"}\n\n")
		}
		for _, e := range evs {
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "id: %d\nevent: out\ndata: %s\n\n", e.Seq, b)
			cursor = e.Seq + 1
		}
		if key := fmt.Sprint(state, code); key != last {
			last = key
			fmt.Fprintf(w, "event: state\ndata: {\"state\":%q,\"code\":%d}\n\n", state, code)
		}
		fl.Flush()
		if state == stateDone {
			return
		}
		keep.Reset(15 * time.Second)
		select {
		case <-ch:
		case <-r.Context().Done():
			return
		case <-s.quit:
			return
		case <-keep.C:
			fmt.Fprint(w, ": keep\n\n")
		}
	}
}

// endRuns asks every run to end (like Ctrl-C) and waits a little for them: the server ends.
func (s *Server) endRuns(ctx context.Context) {
	s.runs.mu.Lock()
	all := slices.Clone(s.runs.runs)
	s.runs.mu.Unlock()
	for _, r := range all {
		r.cancel()
	}
	defer func() {
		for _, r := range all {
			if r.current() == stateDone {
				r.files.remove()
			}
		}
	}()
	for _, r := range all {
		for r.current() != stateDone {
			select {
			case <-ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
}
