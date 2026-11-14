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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
)

// The subcommands of viper-nats-mcp that viper runs over ssh: watch, send, broadcast and request.
// They speak as this machine, with its key and its rights, say nothing in the group (no presence) and
// print one JSON line per event on stdout, so viper never has to read the output of the nats tool,
// where the text of a message can look like a line of its own. The text of a message comes from stdin.

// Line is one line of the output of a subcommand.
type Line = model.NATSLine

// IsCLICommand tells whether the first argument names a subcommand: watch, send, broadcast or request.
func IsCLICommand(name string) bool {
	switch name {
	case "watch", "send", "broadcast", "request", "fetch":
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
	all       bool
	attach    bool   // send, broadcast, request: standard input holds a file after the text (AttachFrame)
	id, sha   string // fetch: the object and the checksum from the message
	out       string // fetch: the folder
}

// cliFlags defines the options of a subcommand.
func cliFlags(fs *flag.FlagSet, name string, a *cliArgs) {
	switch name {
	case "send":
		fs.StringVar(&a.to, "to", "", "the machine")
		fs.BoolVar(&a.attach, "attach", false, "standard input holds a file after the text")
	case "request":
		fs.StringVar(&a.to, "to", "", "the machine")
		fs.DurationVar(&a.timeout, "timeout", 30*time.Second, "how long to wait for the answer")
		fs.BoolVar(&a.attach, "attach", false, "standard input holds a file after the text")
	case "broadcast":
		fs.StringVar(&a.topic, "topic", "", "the topic")
		fs.BoolVar(&a.attach, "attach", false, "standard input holds a file after the text")
	case "fetch":
		fs.StringVar(&a.id, "id", "", "the ID of the object")
		fs.StringVar(&a.sha, "sha256", "", "the checksum of the file, from the message")
		fs.StringVar(&a.out, "out", "", "the folder to put the file in")
	case "watch":
		fs.DurationVar(&a.forTime, "for", 0, "stop after this time (default: until interrupted)")
		fs.BoolVar(&a.all, "all", false, "also show the messages between the other machines")
		fs.BoolVar(&a.query, "query", false, "ask the others to say that they are there (this machine then counts as present for a while)")
	}
}

func parseCLIArgs(name string, args []string, stderr io.Writer) (cliArgs, error) {
	var a cliArgs
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cliFlags(fs, name, &a)
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
	if name == "fetch" && (a.id == "" || a.sha == "" || a.out == "") {
		return a, errors.New("--id, --sha256 and --out are needed")
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
	var file *Framed
	switch {
	case name == "watch" || name == "fetch":
	case a.attach:
		if text, file, err = ReadFrame(stdin, cfg.MaxText, cfg.ObjectsFileMax); err != nil {
			fmt.Fprintln(stderr, "viper-nats-mcp "+name+":", err)
			return 1
		}
	default:
		if text, err = readText(stdin, cfg.MaxText); err != nil {
			fmt.Fprintln(stderr, "viper-nats-mcp "+name+":", err)
			return 1
		}
	}
	if err := runCLI(ctx, cfg, name, a, text, file, stdout, stderr); err != nil {
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

func writeLine(w io.Writer, l Line) error {
	b, _ := json.Marshal(l) // strings and booleans only
	_, err := fmt.Fprintf(w, "%s\n", b)
	return err
}

func runCLI(ctx context.Context, cfg Config, name string, a cliArgs, text string, file *Framed, stdout, stderr io.Writer) error {
	if name == "watch" {
		return watch(ctx, cfg, a, stdout, stderr)
	}
	s, err := connectOnce(ctx, cfg, stderr, nil)
	if err != nil {
		return err
	}
	defer s.nc.Close()
	if name == "fetch" {
		path, err := s.FetchTo(ctx, a.id, a.sha, a.out)
		if err == nil {
			_ = writeLine(stdout, Line{Event: "fetched", Text: Clean(path)})
		}
		return err
	}
	var att *Attachment
	if file != nil {
		if att, err = s.prepareFile(ctx, a, file, stderr); err != nil {
			return err
		}
	}
	switch name {
	case "send":
		err = s.sendMessage(a.to, text, att)
	case "broadcast":
		err = s.broadcastMessage(a.topic, text, att)
	case "request":
		var e Envelope
		if e, err = s.askWith(ctx, a.to, text, att, a.timeout); err == nil {
			_ = writeLine(stdout, Line{Event: "answer", From: a.to, Re: e.Re, Text: Clean(e.Text)})
		}
		return s.settle(err)
	}
	if err == nil {
		l := Line{Event: "sent"}
		setAtt(&l, att)
		_ = writeLine(stdout, l)
	}
	return s.settle(err)
}

// setAtt puts the reference of an attachment into a line of output.
func setAtt(l *Line, a *Attachment) {
	if a != nil {
		l.AttID, l.AttType, l.AttName, l.AttSize, l.AttSHA = a.ID, a.Type, a.Name, a.Size, a.SHA256
	}
}

// prepareFile puts the file that came with the command into the object store: after it has seen that the receivers understand
// attachments. This connection says nothing in the group, so it asks the others to say who they are and what they can do (the
// answer to the question is their usual announcement) and waits for it.
func (s *Service) prepareFile(ctx context.Context, a cliArgs, file *Framed, stderr io.Writer) (*Attachment, error) {
	if !s.HasObjects() {
		return nil, errors.New("the group has no object store (NATS_OBJECTS): attachments are not possible")
	}
	if err := s.publish(s.cfg.Names.Presence(), Envelope{V: 1, Kind: "presence", State: "up", Query: true}); err != nil {
		return nil, err
	}
	to := []string{}
	if a.to != "" {
		to = append(to, a.to)
	}
	s.waitForAnnouncements(ctx, to, a.topic != "")
	unknown, err := s.receiversUnderstand(to, a.topic)
	if err != nil {
		return nil, err
	}
	if len(unknown) > 0 {
		fmt.Fprintf(stderr, "warning: %s have not said which topics they receive; if one of them is an older viper-nats-mcp it refuses the message\n", strings.Join(unknown, ", "))
	}
	return s.PutData(ctx, file.Data, file.Name)
}

// waitForAnnouncements waits until the machines have said what they can do (and, for a topic, which topics they receive), at most
// a few seconds: they answer a query after up to two seconds.
func (s *Service) waitForAnnouncements(ctx context.Context, to []string, topic bool) {
	start := time.Now()
	for time.Since(start) < 10*time.Second && ctx.Err() == nil { // a machine answers a query after up to 2 seconds, and only every 5
		if s.announced(to) && (!topic || time.Since(start) >= 3*time.Second) { // for a topic: whoever is there has had time to say it
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// announced says whether every machine of to has said what it can do.
func (s *Service) announced(to []string) bool {
	known := map[string]bool{}
	for _, p := range s.peers.Present(s.now()) {
		known[p.Name] = p.CapsKnown
	}
	for _, n := range to {
		if !known[n] {
			return false
		}
	}
	return true
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
	// whoever reads the lines is gone (the ssh connection broke): stop, so the program does not stay on the machine
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := &lockedLines{w: stdout}
	cfg.TapAll = a.all
	tap := func(m Seen) {
		if out.write(withAtt(Line{Event: "message", From: m.In.From, To: m.In.To, Via: m.In.Kind, Topic: m.In.Arg, Type: m.E.Kind, ID: m.E.ID,
			Re: m.E.Re, State: m.E.State, Query: m.E.Query, Text: Clean(m.E.Text)}, m.E.Att)) != nil {
			cancel()
		}
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
	if out.write(Line{Event: "ready", Me: cfg.Names.Me, Group: cfg.Names.Group}) != nil {
		return nil
	}
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

func (l *lockedLines) write(x Line) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return writeLine(l.w, x)
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

// withAtt is the line with the reference of the attachment of the message it describes.
func withAtt(l Line, a *Attachment) Line {
	setAtt(&l, a)
	return l
}
