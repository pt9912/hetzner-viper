package natsmcp

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// The subcommands of viper-nats-mcp that viper runs over ssh: watch, send, broadcast and request.
// They speak as this machine, with its key and its rights, say nothing in the group (no presence) and
// print one JSON line per event on stdout, so viper never has to read the output of the nats tool,
// where the text of a message can look like a line of its own. The text of a message comes from stdin.

// Line is one line of the output of a subcommand.
type Line struct {
	Event string `json:"event"`           // ready, message, sent, answer
	Me    string `json:"me,omitempty"`    // ready: this machine
	Group string `json:"group,omitempty"` // ready
	From  string `json:"from,omitempty"`  // message, answer: the sender, as the subject proves it
	Via   string `json:"via,omitempty"`   // message: out, pub or presence
	Topic string `json:"topic,omitempty"` // message via pub
	Type  string `json:"type,omitempty"`  // message: the kind of the envelope (msg, req, res, presence)
	ID    string `json:"id,omitempty"`
	Re    string `json:"re,omitempty"`
	State string `json:"state,omitempty"` // presence
	Query bool   `json:"query,omitempty"` // presence
	Text  string `json:"text,omitempty"`  // always cleaned (Clean)
}

// IsCLICommand tells whether the first argument names a subcommand: watch, send, broadcast or request.
func IsCLICommand(name string) bool {
	switch name {
	case "watch", "send", "broadcast", "request":
		return true
	}
	return false
}

// cliArgs are the options of the subcommands.
type cliArgs struct {
	to, topic string
	timeout   time.Duration
	forTime   time.Duration
	query     bool
}

func parseCLIArgs(name string, args []string, stderr io.Writer) (cliArgs, error) {
	var a cliArgs
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	switch name {
	case "send":
		fs.StringVar(&a.to, "to", "", "the machine")
	case "request":
		fs.StringVar(&a.to, "to", "", "the machine")
		fs.DurationVar(&a.timeout, "timeout", 30*time.Second, "how long to wait for the answer")
	case "broadcast":
		fs.StringVar(&a.topic, "topic", "", "the topic")
	case "watch":
		fs.DurationVar(&a.forTime, "for", 0, "stop after this time (default: until interrupted)")
		fs.BoolVar(&a.query, "query", false, "ask the others to say that they are there (this machine then counts as present for a while)")
	}
	if err := fs.Parse(args); err != nil {
		return a, err
	}
	if fs.NArg() > 0 {
		return a, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if (name == "send" || name == "request") && a.to == "" {
		return a, errors.New("--to is needed")
	}
	if name == "broadcast" && a.topic == "" {
		return a, errors.New("--topic is needed")
	}
	if a.forTime < 0 {
		return a, errors.New("--for must not be negative")
	}
	return a, nil
}

// connectOnce connects, without presence and without the topics of mcp.env, and waits for the connection.
func connectOnce(ctx context.Context, cfg Config, stderr io.Writer, tap func(Seen)) (*Service, error) {
	cfg.Quiet = true
	cfg.Topics = nil
	cfg.LogFile = ""
	s := NewService(cfg, NewFileLogger(stderr, "", 0), nil)
	s.tap = tap
	if err := s.Connect(ctx); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(10 * time.Second)
	for !s.Connected() {
		if time.Now().After(deadline) || ctx.Err() != nil {
			s.nc.Close()
			return nil, errors.New("not connected to the NATS server (" + s.connectionState() + ")")
		}
		time.Sleep(50 * time.Millisecond)
	}
	return s, nil
}

// RunCLI runs a subcommand and returns the exit code.
func RunCLI(ctx context.Context, cfg Config, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	stderr = &lockedWriter{w: stderr} // the callbacks of the connection write to it as well
	a, err := parseCLIArgs(name, args, stderr)
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stderr, "viper-nats-mcp "+name+":", err)
		}
		return 2
	}
	var text string
	if name != "watch" {
		if text, err = readText(stdin, cfg.MaxText); err != nil {
			fmt.Fprintln(stderr, "viper-nats-mcp "+name+":", err)
			return 1
		}
	}
	if err := runCLI(ctx, cfg, name, a, text, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, "viper-nats-mcp "+name+":", err)
		return 1
	}
	return 0
}

func readText(in io.Reader, limit int) (string, error) {
	b, err := io.ReadAll(io.LimitReader(in, int64(limit)+1))
	if err != nil {
		return "", err
	}
	if len(b) > limit {
		return "", fmt.Errorf("the text has more than %d bytes", limit)
	}
	return string(b), nil
}

func writeLine(w io.Writer, l Line) {
	b, _ := json.Marshal(l) // strings and booleans only
	fmt.Fprintf(w, "%s\n", b)
}

func runCLI(ctx context.Context, cfg Config, name string, a cliArgs, text string, stdout, stderr io.Writer) error {
	if name == "watch" {
		return watch(ctx, cfg, a, stdout, stderr)
	}
	s, err := connectOnce(ctx, cfg, stderr, nil)
	if err != nil {
		return err
	}
	defer s.nc.Close()
	switch name {
	case "send":
		err = s.SendText(a.to, text)
	case "broadcast":
		err = s.BroadcastText(a.topic, text)
	case "request":
		var e Envelope
		if e, err = s.Ask(ctx, a.to, text, a.timeout); err == nil {
			writeLine(stdout, Line{Event: "answer", From: a.to, Re: e.Re, Text: Clean(e.Text)})
		}
		return s.settle(err)
	}
	if err == nil {
		writeLine(stdout, Line{Event: "sent"})
	}
	return s.settle(err)
}

// settle waits until the server has the messages and turns an error it reported meanwhile (a refused
// subject) into an error of the command: the server tells a refusal only afterwards, and not the call that sent.
func (s *Service) settle(err error) error {
	if err != nil {
		return err
	}
	if ferr := s.nc.FlushTimeout(3 * time.Second); ferr != nil {
		return ferr
	}
	time.Sleep(200 * time.Millisecond)
	if n := s.errorCount(); n > 0 {
		return fmt.Errorf("the server reported %d error(s) (see above): the message may not have been delivered", n)
	}
	return nil
}

func (s *Service) errorCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errs
}

func watch(ctx context.Context, cfg Config, a cliArgs, stdout, stderr io.Writer) error {
	if a.forTime > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.forTime)
		defer cancel()
	}
	out := &lockedLines{w: stdout}
	tap := func(m Seen) {
		out.write(Line{Event: "message", From: m.In.From, Via: m.In.Kind, Topic: m.In.Arg, Type: m.E.Kind, ID: m.E.ID,
			Re: m.E.Re, State: m.E.State, Query: m.E.Query, Text: Clean(m.E.Text)})
	}
	s, err := connectOnce(ctx, cfg, stderr, tap)
	if err != nil {
		return err
	}
	defer s.nc.Close()
	if a.query {
		if err := s.publish(s.cfg.Names.Presence(), Envelope{V: 1, Kind: "presence", State: "up", Query: true}); err != nil {
			return err
		}
	}
	out.write(Line{Event: "ready", Me: cfg.Names.Me, Group: cfg.Names.Group})
	<-ctx.Done()
	return nil
}

// cliMain is Main for a subcommand.
func cliMain(name string, cfg Config, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return RunCLI(ctx, cfg, name, args, stdin, stdout, stderr)
}

// lockedLines writes lines from the goroutines of the connection one at a time.
type lockedLines struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedLines) write(x Line) {
	l.mu.Lock()
	defer l.mu.Unlock()
	writeLine(l.w, x)
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
