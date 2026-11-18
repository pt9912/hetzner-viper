package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/chatweb"
	"github.com/pt9912/hetzner-viper/internal/adapter/driving/web"
	"github.com/pt9912/hetzner-viper/internal/filelock"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/natsmcp"
	"github.com/pt9912/hetzner-viper/internal/port/driven/chatlog"
	"github.com/pt9912/hetzner-viper/internal/port/driven/chatstream"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

// nativeUIChat is viper ui-chat: the chat of docs/plan/ui-chat.md in the browser. Like viper ui it is a web server on 127.0.0.1
// with a key that works once; unlike it, it has no runs and no edits.
func nativeUIChat(o *options) error {
	env, err := o.env()
	if err != nil {
		return err
	}
	dir := filepath.Join(model.StateDir(env.Home), "chat")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	unlock, err := filelock.TryLock(filepath.Join(dir, ".lock"))
	if errors.Is(err, filelock.ErrBusy) {
		return fmt.Errorf("viper ui-chat: it runs already%s (one at a time: both would write the same history)", runningPort(dir))
	}
	if err != nil {
		return err
	}
	defer unlock()
	secrets := &secretValues{}
	b := &uiBackend{o: o, env: env, secrets: secrets}
	b.learnEnvironment()
	store, err := o.host().Services.ChatStore(dir, o.keepDays, o.noHistory)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	cb := &chatBackend{b: b}
	engine := o.host().Services.Chat(store, &chatConnector{o: o, env: env}, natsmcp.Clean, secrets.redact)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); engine.Stop() }()
	go pruneLoop(ctx, store, cb)
	cfg := web.Config{Version: o.version, Readonly: o.readonly, Clean: natsmcp.Clean, Redact: secrets.redact, LongestSecret: secrets.longest,
		Chat: chatweb.New(cb, chatweb.Options{Readonly: o.readonly, NoPairs: o.noPairs, Version: o.version, Engine: engine, Store: store})}
	return serveBrowser(o, cfg, "viper ui-chat", func(port string) {
		_ = os.WriteFile(filepath.Join(dir, ".port"), []byte(port+"\n"), 0o600)
	}, func() { _ = os.Remove(filepath.Join(dir, ".port")) })
}

// pruneLoop takes what is too old or too much out of the history of every group, now and then.
func pruneLoop(ctx context.Context, store chatlog.Store, cb *chatBackend) {
	for {
		if gs, err := cb.Groups(ctx); err == nil {
			for _, g := range gs {
				_ = store.Prune(g.Name, time.Now())
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Hour):
		}
	}
}

// chatConnector opens the connection of the operator to a group, as viper nats does: an ssh tunnel to the server, and the stream
// of watch --all --query through it.
type chatConnector struct {
	o   *options
	env settings.Env
}

func (c *chatConnector) Connect(ctx context.Context, group string) (chatstream.Conn, error) { //nolint:ireturn // the contract of chatstream.Connector
	oc := *c.o // loadNatsGroup writes the group into the options: a copy keeps the streams apart
	g, err := loadNatsGroup(&oc, c.env, group)
	if err != nil {
		return nil, err
	}
	dead := make(chan struct{})
	sess, err := openOperatorDead(ctx, &oc, g, dead)
	if err != nil {
		return nil, err
	}
	return &chatConn{sess: sess, dead: dead}, nil
}

type chatConn struct {
	sess *operatorSession
	dead chan struct{}
}

func (c *chatConn) Watch(ctx context.Context, stdout, stderr io.Writer) error {
	return c.sess.remote(ctx, "", "", stdout, stderr, "viper-nats-mcp", "watch", "--all", "--query")
}
func (c *chatConn) Dead() <-chan struct{} { return c.dead }
func (c *chatConn) Close()                { c.sess.close() }

// runningPort says the port of the viper ui-chat that holds the lock, if it wrote it down.
func runningPort(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, ".port"))
	if err != nil {
		return ""
	}
	if p := strings.TrimSpace(string(b)); p != "" {
		if _, err := strconv.Atoi(p); err == nil {
			return " (port " + p + ")"
		}
	}
	return ""
}

// chatBackend tells the chat of the NATS groups of this computer: from the settings, as viper nats reads them.
type chatBackend struct {
	b     *uiBackend
	mu    sync.Mutex
	at    time.Time
	cache []chatweb.Group
}

// Groups are asked for by every request of the page; what the settings say is read again after a few seconds, not each time.
func (c *chatBackend) Groups(ctx context.Context) ([]chatweb.Group, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cache != nil && time.Since(c.at) < 3*time.Second {
		return c.cache, nil
	}
	gs, err := c.load(ctx)
	if err != nil {
		return nil, err
	}
	c.cache, c.at = gs, time.Now()
	return gs, nil
}

func (c *chatBackend) load(ctx context.Context) ([]chatweb.Group, error) {
	ov, err := c.b.Overview(ctx)
	if err != nil {
		return nil, err
	}
	var out []chatweb.Group
	for _, ng := range c.b.natsGroups(ov) {
		g, err := c.group(ng)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

// group describes one NATS group: its machines, the topics of their settings, and the name of the operator.
func (c *chatBackend) group(ng web.NATSGroup) (chatweb.Group, error) {
	g := chatweb.Group{Name: ng.Name, Operator: ng.Operator, FileMax: ng.FileMax, OperatorName: model.DefaultNATSOperator}
	oc := *c.b.o // loadNatsGroup writes the group into the options: a copy, as requests run at the same time
	loaded, err := loadNatsGroup(&oc, c.b.env, ng.Name)
	if err != nil {
		return g, fmt.Errorf("%s: %w", ng.Name, err)
	}
	g.OperatorName = loaded.operator
	topics := map[string]bool{}
	for _, name := range loaded.order {
		m, ok := loaded.ms[name]
		if !ok {
			continue
		}
		cm := chatweb.Machine{Name: name, Topics: m.cfg.NATS.MCPTopics}
		for _, from := range m.cfg.NATS.MCPPushFrom {
			cm.PushFromOperator = cm.PushFromOperator || from == loaded.operator
		}
		for _, t := range m.cfg.NATS.MCPTopics {
			topics[t] = true
		}
		g.Machines = append(g.Machines, cm)
	}
	for t := range topics {
		g.Topics = append(g.Topics, t)
	}
	sort.Strings(g.Topics)
	return g, nil
}

// Send speaks as the operator, as viper nats does: an ssh tunnel to the server and the subcommand of viper-nats-mcp in this program.
// The text goes in as the input of the subcommand, never into a command line. A question is asked in the background: the answer
// comes in the stream, and done tells that the waiting is over.
func (c *chatBackend) Send(ctx context.Context, group string, o chatweb.Outgoing, done func(id string, err error)) (string, error) {
	oc := *c.b.o
	g, err := loadNatsGroup(&oc, c.b.env, group)
	if err != nil {
		return "", err
	}
	if _, ok := g.ms[o.To]; o.Kind != "topic" && !ok { // the subcommand takes any name; the machines of the group are the chat's to check
		return "", fmt.Errorf("%q is no machine of the group %s", o.To, group)
	}
	words := []string{"viper-nats-mcp"}
	switch o.Kind {
	case "msg":
		words = append(words, "send", "--to", o.To)
	case "topic":
		words = append(words, "broadcast", "--topic", o.Topic)
	case "reply":
		words = append(words, "reply", "--to", o.To, "--re", o.Re)
	case "ask":
		words = append(words, "request", "--to", o.To, "--timeout", strconv.Itoa(int(o.Timeout.Seconds()))+"s", "--announce")
	default:
		return "", fmt.Errorf("unknown kind %q", o.Kind)
	}
	if o.Kind == "ask" {
		return c.ask(&oc, g, o, words, done) //nolint:contextcheck // the waiting goes on after the request of the page is over
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second) // a hung tunnel must not hold a place for ever
	defer cancel()
	sess, err := openOperator(ctx, &oc, g)
	if err != nil {
		return "", err
	}
	defer sess.close()
	var stdout, stderr bytes.Buffer
	if err := sess.remote(ctx, "", o.Text, &stdout, &stderr, words...); err != nil {
		return "", sendError(&stderr, err)
	}
	return "", nil
}

// sendError is what the command said on stderr, or the error.
func sendError(stderr *bytes.Buffer, err error) error {
	if s := strings.TrimSpace(stderr.String()); s != "" {
		lines := strings.Split(s, "\n")
		return errors.New(natsmcp.Clean(lines[len(lines)-1]))
	}
	return err
}

// ask runs the question in the background and returns its ID as soon as the command has written it (event asked).
func (*chatBackend) ask(oc *options, g *natsGroup, o chatweb.Outgoing, words []string, done func(string, error)) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), o.Timeout+30*time.Second) // not the request of the page: the waiting goes on after it
	sess, err := openOperator(ctx, oc, g)
	if err != nil {
		cancel()
		return "", err
	}
	pr, pw := io.Pipe()
	ids := make(chan string, 1)
	scanned := make(chan struct{})
	go func() { // the lines of the command: the ID of the question is the first
		defer close(scanned)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		defer func() { _, _ = io.Copy(io.Discard, pr) }() // the command must never wait for a reader that has stopped
		for sc.Scan() {
			var l model.NATSLine
			if json.Unmarshal(sc.Bytes(), &l) == nil && l.Event == "asked" {
				select {
				case ids <- l.ID:
				default:
				}
			}
		}
	}()
	var stderr bytes.Buffer
	var mu sync.Mutex
	var id string
	finished := make(chan error, 1)
	go func() {
		err := sess.remote(ctx, "", o.Text, pw, &lockedBuffer{b: &stderr, mu: &mu}, words...)
		_ = pw.Close()
		sess.close()
		cancel()
		if err != nil {
			mu.Lock()
			err = sendError(&stderr, err)
			mu.Unlock()
		}
		finished <- err
	}()
	select {
	case id = <-ids:
		go func() {
			err := <-finished
			done(id, err)
		}()
		return id, nil
	case err := <-finished:
		<-scanned // the command ended: what it wrote is read
		select {
		case id = <-ids: // asked and over at once
			go func() { done(id, err) }()
			return id, nil
		default:
		}
		if err == nil {
			err = errors.New("the question was not sent")
		}
		return "", err
	}
}

// lockedBuffer is a buffer that the command and this function use at the same time.
type lockedBuffer struct {
	b  *bytes.Buffer
	mu *sync.Mutex
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
