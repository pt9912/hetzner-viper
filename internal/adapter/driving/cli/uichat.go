package cli

import (
	"context"
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
