package natsmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

const (
	presenceEvery = 30 * time.Second
	presenceTTL   = 90 * time.Second
	maxTopics     = 16
)

// Service connects to the NATS server and keeps what the tools work with.
type Service struct {
	cfg   Config
	log   Logger
	inbox *Inbox
	peers *Peers
	now   func() time.Time

	// notify pushes a message into the session (nil: no channel); jitter waits before an answer to a presence query.
	notify func(content string, meta map[string]string) error
	jitter func() time.Duration

	nc *nats.Conn

	mu      sync.Mutex
	waiters map[string]*waiter
	topics  map[string]*nats.Subscription
	pushed  map[string][]time.Time
	errs    int
}

type waiter struct {
	to string
	ch chan Envelope
}

// NewService returns a service for the configuration. Connect starts it.
func NewService(cfg Config, log Logger, notify func(string, map[string]string) error) *Service {
	return &Service{cfg: cfg, log: log, notify: notify, now: time.Now,
		inbox: NewInbox(cfg.InboxMax, cfg.InboxPerMan), peers: NewPeers(presenceTTL),
		jitter:  func() time.Duration { return time.Duration(rand.Int64N(int64(2 * time.Second))) },
		waiters: map[string]*waiter{}, topics: map[string]*nats.Subscription{}, pushed: map[string][]time.Time{}}
}

// Connect connects to the NATS server, with unlimited retries, and subscribes. If the server
// cannot be reached yet it returns anyway; the tools then say that they are not connected.
func (s *Service) Connect(ctx context.Context) error {
	seed, err := os.ReadFile(s.cfg.SeedFile)
	if err != nil {
		return fmt.Errorf("reading the key: %w", err)
	}
	kp, err := nkeys.FromSeed([]byte(strings.TrimSpace(string(seed))))
	if err != nil {
		return fmt.Errorf("the key file holds no NKey seed: %w", err)
	}
	pub, err := kp.PublicKey()
	if err != nil {
		return err
	}
	opts := []nats.Option{
		nats.Name("viper-nats-mcp"), nats.Nkey(pub, kp.Sign), nats.NoEcho(),
		nats.RetryOnFailedConnect(true), nats.MaxReconnects(-1), nats.ReconnectWait(2 * time.Second),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) { s.logError("server error", err) }),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) { s.logError("disconnected", err) }),
		nats.ReconnectHandler(func(*nats.Conn) { s.log.Printf("reconnected") }),
	}
	if s.cfg.CAFile != "" {
		opts = append(opts, nats.RootCAs(s.cfg.CAFile))
	}
	nc, err := nats.Connect(s.cfg.URL, opts...)
	if err != nil {
		return err
	}
	s.nc = nc
	if _, err := nc.Subscribe(s.cfg.Names.InSub(), s.onOut); err != nil {
		return err
	}
	if _, err := nc.Subscribe(s.cfg.Names.PresenceSub(), s.onPresence); err != nil {
		return err
	}
	go s.presenceLoop(ctx)
	s.publishPresence("up", true)
	return nil
}

// logError logs the first errors of a kind and then only some, so a flood of refused messages
// does not fill the log.
func (s *Service) logError(what string, err error) {
	s.mu.Lock()
	s.errs++
	n := s.errs
	s.mu.Unlock()
	if n <= 20 || n%100 == 0 {
		s.log.Printf("%s: %v (%d so far)", what, err, n)
	}
}

// Close says goodbye and closes the connection.
func (s *Service) Close() {
	if s.nc == nil {
		return
	}
	s.publishPresence("bye", false)
	_ = s.nc.Flush()
	s.nc.Close()
}

// Connected tells whether the connection to the NATS server is up.
func (s *Service) Connected() bool { return s.nc != nil && s.nc.Status() == nats.CONNECTED }

func (s *Service) presenceLoop(ctx context.Context) {
	t := time.NewTicker(presenceEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.publishPresence("up", false)
		}
	}
}

func (s *Service) publishPresence(state string, query bool) {
	if s.Connected() {
		_ = s.publish(s.cfg.Names.Presence(), Envelope{V: 1, Kind: "presence", State: state, Query: query})
	}
}

func (s *Service) publish(subject string, e Envelope) error {
	if !s.Connected() {
		return errors.New("not connected to the NATS server")
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return s.nc.Publish(subject, b)
}

// onOut handles a message addressed to this machine.
func (s *Service) onOut(m *nats.Msg) {
	in, ok := s.cfg.Names.Parse(m.Subject)
	if !ok || in.Kind != "out" {
		return
	}
	e, err := Decode(m.Data, s.cfg.MaxText)
	if err != nil {
		s.log.Printf("refused a message from %s: %v", in.From, err)
		return
	}
	s.log.Printf("message from %s: kind=%s bytes=%d", in.From, e.Kind, len(m.Data))
	switch e.Kind {
	case "msg", "req":
		s.receive(Entry{From: in.From, Kind: e.Kind, ID: e.ID, Text: e.Text, TS: s.now()})
	case "res":
		// an answer counts only from the machine that was asked
		s.mu.Lock()
		w := s.waiters[e.Re]
		ok := w != nil && w.to == in.From
		s.mu.Unlock()
		if ok {
			select {
			case w.ch <- e:
			default:
			}
		} else {
			s.log.Printf("answer from %s to no open request of theirs", in.From)
		}
	}
}

// onTopic handles a message on a topic this machine subscribed to.
func (s *Service) onTopic(m *nats.Msg) {
	in, ok := s.cfg.Names.Parse(m.Subject)
	if !ok || in.Kind != "pub" {
		return
	}
	e, err := Decode(m.Data, s.cfg.MaxText)
	if err != nil || e.Kind != "msg" {
		s.log.Printf("refused a topic message from %s", in.From)
		return
	}
	s.log.Printf("topic message from %s: topic=%s bytes=%d", in.From, in.Arg, len(m.Data))
	s.receive(Entry{From: in.From, Kind: "msg", Topic: in.Arg, Text: e.Text, TS: s.now()})
}

func (s *Service) onPresence(m *nats.Msg) {
	in, ok := s.cfg.Names.Parse(m.Subject)
	if !ok || in.Kind != "presence" {
		return
	}
	e, err := Decode(m.Data, s.cfg.MaxText)
	if err != nil || e.Kind != "presence" {
		return
	}
	if e.State == "bye" {
		s.peers.Gone(in.From)
		return
	}
	s.peers.Seen(in.From, s.now())
	if e.Query { // somebody started: let it know that we are here, not all at the same moment
		time.AfterFunc(s.jitter(), func() { s.publishPresence("up", false) })
	}
}

// receive stores a message and pushes it if the policy lets it.
func (s *Service) receive(e Entry) {
	e = s.inbox.Add(e)
	s.maybePush(e)
}

// maybePush gives a message to the agent at once if its sender is on the list and has not used up
// its rate. The attributes come from the server; the text cannot add any.
func (s *Service) maybePush(e Entry) {
	if s.notify == nil || !s.cfg.PushFrom[e.From] {
		return
	}
	now := s.now()
	s.mu.Lock()
	recent := s.pushed[e.From][:0]
	for _, t := range s.pushed[e.From] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= s.cfg.PushPerMin {
		s.pushed[e.From] = recent
		s.mu.Unlock()
		s.log.Printf("push from %s over its rate; the message stays in the inbox", e.From)
		return
	}
	s.pushed[e.From] = append(recent, now)
	s.mu.Unlock()
	meta := map[string]string{"from": e.From, "kind": e.Kind, "handle": fmt.Sprint(e.Handle), "ts": e.TS.UTC().Format(time.RFC3339)}
	if e.Topic != "" {
		meta["topic"] = e.Topic
	}
	if err := s.notify(Safe(e.Text), meta); err != nil {
		s.log.Printf("push failed: %v", err)
	}
}
