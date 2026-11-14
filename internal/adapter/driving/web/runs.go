package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
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
	maxRuns        = 4                      // running at the same time
	keepFinished   = 20
	maxRetained    = 1 << 20 // what of the output of a run is kept for a page that attaches late
	maxAnswer      = 256
	maxPendingLine = 4096
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
	Names   []string `json:"-"` // the machines the run holds

	mu      sync.Mutex
	proc    Proc
	events  []event
	size    int
	next    int
	state   string
	code    int
	started time.Time
	notify  chan struct{} // closed and replaced at every change
	kill    *time.Timer
	files   *runFiles // what the run has on this computer: the text, a file to send, the folder of a fetch
}

// view is what a page needs: the events from the cursor on, the state, and a channel that closes at the next change.
func (r *run) view(cursor int) (evs []event, state string, code int, ch <-chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if e.Seq >= cursor {
			evs = append(evs, e)
		}
	}
	return evs, r.state, r.code, r.notify
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
	State   string `json:"state"`
	Code    int    `json:"code"`
	Started string `json:"started"`
}

func (r *run) info() info {
	r.mu.Lock()
	defer r.mu.Unlock()
	return info{r.ID, r.Action, r.Machine, r.Group, r.state, r.code, r.started.Format(time.RFC3339)}
}

// Runs: a table on the server, not in a page: a page that is closed ends nothing, and a new one finds the runs again.
type runTable struct {
	mu   sync.Mutex
	runs []*run
	held map[string]string // machine -> the run that has it
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

// repoUsable: a repository belongs to up and preflight, and is one word without control characters that is no option.
func repoUsable(req runRequest) bool {
	if req.Repo == "" {
		return true
	}
	return req.Action != "down" && len(req.Repo) <= 512 && !strings.HasPrefix(req.Repo, "-") && !strings.ContainsFunc(req.Repo, func(c rune) bool { return c <= ' ' || c == 0x7f })
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
	ov, err := s.cfg.Backend.Overview(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, s.show(err.Error()))
		return
	}
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
	run := &run{files: files, ID: random(), Action: req.Action, Machine: req.Machine, Group: req.Group, Names: names, state: stateRunning, started: s.cfg.Now(), notify: make(chan struct{})}
	if req.Scope == "group" {
		run.Machine = ""
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
	run.proc = proc
	run.add("$ viper " + strings.Join(argv, " ") + "\n")
	go s.pump(run)
	reply(w, map[string]string{"id": run.ID})
}

// hold puts the run in the table if the machines are free: one run per machine, and a limit on the runs at once.
func (s *Server) hold(r *run) (int, string) {
	t := &s.runs
	t.mu.Lock()
	defer t.mu.Unlock()
	active := 0
	for _, o := range t.runs {
		if o.current() != stateDone {
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
		if o == r && r.proc == nil { // never started
			t.runs = append(t.runs[:i], t.runs[i+1:]...)
			break
		}
	}
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

// pump shows the output as it comes: whole lines at once, a line that stays open (a question) after a short silence.
func (s *Server) pump(r *run) {
	chunks := readChunks(r.proc.Output())
	var pending []byte
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
			pending = s.lines(r, append(pending, c...))
			if len(pending) > 0 {
				timer.Reset(flushAfter)
			} else {
				timer.Stop()
			}
			r.setState(stateRunning)
		case <-timer.C:
			if len(pending) > 0 {
				r.add(s.show(string(pending)))
				pending = nil
				r.setState(stateWaiting)
			}
		}
	}
	if len(pending) > 0 {
		r.add(s.show(string(pending)))
	}
	s.finish(r, r.proc.Wait())
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

// lines shows what ends in a new line and returns the rest.
func (s *Server) lines(r *run, b []byte) []byte {
	if i := strings.LastIndexByte(string(b), '\n'); i >= 0 {
		r.add(s.show(string(b[:i+1])))
		b = b[i+1:]
	}
	if len(b) > maxPendingLine {
		cut := len(b)
		for i := max(0, len(b)-3); i < len(b); i++ {
			if utf8.RuneStart(b[i]) && !utf8.FullRune(b[i:]) {
				cut = i
				break
			}
		}
		r.add(s.show(string(b[:cut])))
		b = b[cut:]
	}
	return b
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
	r.files.finish() // the text and the file that went out; a fetched file stays for the page to take
}

func (r *run) cancel() {
	r.mu.Lock()
	if r.state == stateDone || r.state == stateCancelling {
		r.mu.Unlock()
		return
	}
	r.state = stateCancelling
	r.kill = time.AfterFunc(killAfter, func() { _ = r.proc.Kill() })
	r.changed()
	r.mu.Unlock()
	_ = r.proc.Interrupt()
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
	if run.current() != stateWaiting {
		fail(w, http.StatusConflict, "the run asks nothing now")
		return
	}
	if _, err := io.WriteString(run.proc.Input(), req.Text+"\n"); err != nil {
		fail(w, http.StatusConflict, "the run has ended")
		return
	}
	run.add(s.show(req.Text) + "\n")
	run.setState(stateRunning)
	reply(w, map[string]bool{"ok": true})
}

// events streams the output of a run to a page (server-sent events). It changes nothing: a GET only attaches.
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
	for {
		evs, state, code, ch := run.view(cursor)
		for _, e := range evs {
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "event: out\ndata: %s\n\n", b)
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
		select {
		case <-ch:
		case <-r.Context().Done():
			return
		case <-s.quit:
			return
		case <-time.After(15 * time.Second):
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
