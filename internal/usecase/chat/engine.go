package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
	port "github.com/pt9912/hetzner-viper/internal/port/driven/chatlog"
)

// Options of the engine.
type Options struct {
	Store     port.Store
	Connector Connector
	// Clean makes text fit to show and Redact takes the secrets out of it; both run on the text after the line is read as JSON.
	Clean, Redact func(string) string
	Now           func() time.Time
	// Wait is the first wait before a new try (2 s, doubled up to 30 s); tests make it short.
	Wait time.Duration
}

// Engine reads the streams of the groups, keeps what comes in and tells the pages.
type Engine struct {
	o      Options
	mu     sync.Mutex
	base   context.Context //nolint:containedctx // the life of the engine: its runs end with Stop
	stop   context.CancelFunc
	groups map[string]*run
	subs   map[*Subscription]bool
}

// New makes an engine.
func New(o Options) *Engine {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Clean == nil {
		o.Clean = func(s string) string { return s }
	}
	if o.Redact == nil {
		o.Redact = func(s string) string { return s }
	}
	if o.Wait == 0 {
		o.Wait = 2 * time.Second
	}
	base, stop := context.WithCancel(context.Background())
	return &Engine{o: o, base: base, stop: stop, groups: map[string]*run{}, subs: map[*Subscription]bool{}}
}

// run is the reading of one group.
type run struct {
	g       Group
	name    string
	cancel  context.CancelFunc
	retry   chan struct{}
	pres    *Presence
	lim     Limiter
	adm     Admission
	known   map[string]bool // the conversations that exist
	status  GroupStatus
	lastSeq int64
}

// Ensure starts reading the group if it is not read yet (the operator has the key: the caller says so by calling). A group that is
// read already keeps its run; the machines may change, which Update does.
func (e *Engine) Ensure(name string, g Group) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if r, ok := e.groups[name]; ok {
		r.g = g
		return
	}
	cctx, cancel := context.WithCancel(e.base)
	r := &run{g: g, name: name, cancel: cancel, retry: make(chan struct{}, 1), pres: NewPresence(90 * time.Second), known: map[string]bool{}, status: GroupStatus{Status: StatusConnecting}}
	if cs, err := e.o.Store.Conversations(name); err == nil {
		for _, c := range cs {
			r.known[c.Key] = true
		}
	}
	e.groups[name] = r
	go e.loop(cctx, r)
}

// Retry makes a group that waits for a login (StatusAuth) try again.
func (e *Engine) Retry(name string) {
	e.mu.Lock()
	r := e.groups[name]
	e.mu.Unlock()
	if r != nil {
		select {
		case r.retry <- struct{}{}:
		default:
		}
	}
}

// Stop ends the reading of all groups.
func (e *Engine) Stop() {
	e.stop()
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, r := range e.groups {
		r.cancel()
	}
}

func (e *Engine) setStatus(r *run, s GroupStatus) {
	e.mu.Lock()
	s.Dropped, s.Refused = r.lim.Dropped, r.adm.Denied
	r.status = s
	subs := e.snapshotSubs()
	e.mu.Unlock()
	e.send(subs, Event{Group: r.name, Kind: "status", Status: &s})
}

func (e *Engine) snapshotSubs() []*Subscription {
	out := make([]*Subscription, 0, len(e.subs))
	for s := range e.subs {
		out = append(out, s)
	}
	return out
}

func (e *Engine) loop(ctx context.Context, r *run) {
	wait := e.o.Wait
	for ctx.Err() == nil {
		e.setStatus(r, GroupStatus{Status: StatusConnecting})
		reason, auth := e.once(ctx, r)
		if ctx.Err() != nil {
			return
		}
		if auth {
			e.setStatus(r, GroupStatus{Status: StatusAuth, Reason: ErrAuth.Error()})
			select {
			case <-r.retry:
				wait = e.o.Wait
				continue
			case <-ctx.Done():
				return
			}
		}
		e.setStatus(r, GroupStatus{Status: StatusDisturbed, Reason: reason, RetryIn: int(wait.Seconds())})
		if !sleep(ctx, wait) {
			return
		}
		wait = min(wait*2, 30*time.Second)
	}
}

// once connects and reads until the connection ends; it says why, and whether the server did not know the key.
func (e *Engine) once(ctx context.Context, r *run) (reason string, auth bool) {
	conn, err := e.o.Connector.Connect(ctx, r.name)
	if err != nil {
		return err.Error(), errors.Is(err, ErrAuth)
	}
	auth, reason = e.watch(ctx, r, conn)
	conn.Close()
	return reason, auth
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

// watch reads one connection until it ends; it says whether the server did not know the key, and why it ended.
func (e *Engine) watch(ctx context.Context, r *run, conn Conn) (auth bool, reason string) {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-conn.Dead(): // the tunnel ended: Watch would hang
			cancel()
		case <-wctx.Done():
		}
	}()
	var authSeen, deadSeen bool
	var last string
	var lmu sync.Mutex
	out := &lineWriter{max: 256 << 10, line: func(b []byte) { e.line(r, b) }, ready: func() { e.setStatus(r, GroupStatus{Status: StatusConnected}) }}
	errw := &lineWriter{max: 4 << 10, line: func(b []byte) {
		s := strings.ToLower(string(b))
		lmu.Lock()
		defer lmu.Unlock()
		switch {
		case strings.Contains(s, "authorization violation"):
			authSeen = true
			cancel() // watch itself would go on trying for ever
		case strings.Contains(s, "disconnected") || strings.Contains(s, "server error"):
			last = strings.TrimSpace(string(b))
		}
	}}
	err := conn.Watch(wctx, out, errw)
	select {
	case <-conn.Dead():
		deadSeen = true
	default:
	}
	lmu.Lock()
	defer lmu.Unlock()
	switch {
	case authSeen:
		return true, ErrAuth.Error()
	case deadSeen:
		return false, "the tunnel to the server ended"
	case err != nil:
		return false, e.o.Redact(err.Error())
	case last != "":
		return false, e.o.Redact(last)
	}
	return false, "the stream ended"
}

// line takes one line of the stream: presence is state, a message is classified, limited, cleaned and kept.
func (e *Engine) line(r *run, b []byte) {
	var l model.NATSLine
	if json.Unmarshal(b, &l) != nil {
		return
	}
	now := e.o.Now()
	e.mu.Lock()
	r.pres.Update(l, now)
	route := Classify(r.g, l)
	if route.Skip {
		e.mu.Unlock()
		return
	}
	if !r.lim.Allow(l.From, now) {
		e.mu.Unlock()
		return
	}
	keys := route.Keys
	for _, k := range keys { // a topic that is new needs room, and the sender may not make too many
		if strings.HasPrefix(k, KindTopic+"__") && !r.known[k] && !r.adm.Topic(l.From, len(r.known), now) {
			keys = []string{KindAll}
			break
		}
	}
	e.mu.Unlock()
	rec := port.Record{T: now.UTC(), Dir: "in", From: l.From, To: l.To, Topic: l.Topic, Type: l.Type, ID: l.ID, Re: l.Re, Unknown: route.Unknown,
		Text: e.o.Redact(e.o.Clean(l.Text)), AttID: l.AttID, AttType: l.AttType, AttName: e.o.Redact(e.o.Clean(l.AttName)), AttSize: l.AttSize, AttSHA: l.AttSHA}
	kept, err := e.o.Store.Append(r.name, keys, rec)
	if err != nil {
		return
	}
	e.mu.Lock()
	for _, k := range keys {
		r.known[k] = true
	}
	r.lastSeq = kept.N
	subs := e.snapshotSubs()
	e.mu.Unlock()
	e.send(subs, Event{Group: r.name, Kind: "message", Keys: keys, Record: &kept})
}

// lineWriter cuts what is written to it into lines; a line over the limit is dropped. The first "ready" line calls ready.
type lineWriter struct {
	mu    sync.Mutex
	buf   []byte
	max   int
	skip  bool
	line  func([]byte)
	ready func()
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, c := range p {
		if c == '\n' {
			if !w.skip && len(w.buf) > 0 {
				if w.ready != nil && bytes.HasPrefix(w.buf, []byte(`{"event":"ready"`)) {
					w.ready()
				} else {
					w.line(append([]byte(nil), w.buf...))
				}
			}
			w.buf, w.skip = w.buf[:0], false
			continue
		}
		if w.skip {
			continue
		}
		if len(w.buf) >= w.max {
			w.buf, w.skip = w.buf[:0], true
			continue
		}
		w.buf = append(w.buf, c)
	}
	return len(p), nil
}

// Snapshot of a group; for a group that is not read it says so.
func (e *Engine) Snapshot(name string, topics []string) Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.groups[name]
	if r == nil {
		return Snapshot{Status: GroupStatus{Status: StatusConnecting}, Machines: map[string]MachineState{}, Topics: map[string]TopicState{}}
	}
	now := e.o.Now()
	s := Snapshot{Status: r.status, Machines: map[string]MachineState{}, Topics: map[string]TopicState{}}
	s.Status.Dropped, s.Status.Refused = r.lim.Dropped, r.adm.Denied
	all := true
	for m := range r.g.Machines {
		ago := -1
		if t := r.pres.LastSeen(m); !t.IsZero() {
			ago = int(now.Sub(t).Seconds())
		}
		s.Machines[m] = MachineState{State: r.pres.State(m, now), SeenAgo: ago, Reported: r.pres.Reported(m)}
		all = all && r.pres.Reported(m)
	}
	for _, t := range topics {
		s.Topics[t] = TopicState{Subscribers: r.pres.Subscribers(t), Known: all}
	}
	return s
}

// Listen is Subscribe for the port: the events from now on, and the function that ends the listening.
func (e *Engine) Listen() (events <-chan Event, closeFn func()) {
	sub := e.Subscribe()
	return sub.C, sub.Close
}

// Subscription is a page that listens.
type Subscription struct {
	C      chan Event
	e      *Engine
	closed bool
}

// Subscribe returns the events from now on. Events that a slow page cannot take are dropped (it loads again from the numbers).
func (e *Engine) Subscribe() *Subscription {
	s := &Subscription{C: make(chan Event, 256), e: e}
	e.mu.Lock()
	e.subs[s] = true
	e.mu.Unlock()
	return s
}

// Close ends the subscription.
func (s *Subscription) Close() {
	s.e.mu.Lock()
	defer s.e.mu.Unlock()
	if !s.closed {
		s.closed = true
		delete(s.e.subs, s)
	}
}

func (*Engine) send(subs []*Subscription, ev Event) {
	for _, s := range subs {
		select {
		case s.C <- ev:
		default: // a page that does not read: it asks again from its last number
		}
	}
}
