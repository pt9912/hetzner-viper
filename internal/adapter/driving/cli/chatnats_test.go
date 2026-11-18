package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/pt9912/hetzner-viper/internal/adapter/driving/chatweb"
	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/natsmcp"
	"github.com/pt9912/hetzner-viper/internal/natsserver"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

// chatWorld is a group dev with a server (the operator registered, ssh replaced by a direct address) and the machine beta as an agent
// would be.
type chatWorld struct {
	opts *cli.Opts
	env  settings.Env
	beta *natsmcp.Service
}

func newChatWorld(t *testing.T) *chatWorld {
	t.Helper()
	dir, env := natsGroup(t, "")
	op := &fakeOp{registered: map[string]string{}}
	_, h := fakeTools(t)
	h.Env = func() (settings.Env, error) { return env, nil }
	h.Services.NATSOp = op
	if err := cli.RunCommand("nats", &cli.Opts{Host: h, Dir: dir, Args: []string{"init", "dev"}}); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	id, _ := natsserver.NewIdentity()
	seed := filepath.Join(tmp, "beta.nk")
	if err := os.WriteFile(seed, []byte(id.Seed+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	block, err := natsserver.AuthorizationBlock("dev", []natsserver.Client{
		{Machine: "beta", PublicKey: id.PublicKey}, {Machine: "operator", PublicKey: op.registered["operator"], Operator: true}}, false)
	if err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(tmp, "server.conf")
	if err := os.WriteFile(conf, []byte("host: \"127.0.0.1\"\nport: -1\n\n"+block), 0o600); err != nil {
		t.Fatal(err)
	}
	opts, err := server.ProcessConfigFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("server not ready")
	}
	t.Cleanup(srv.Shutdown)
	beta := natsmcp.NewService(natsmcp.Config{URL: srv.ClientURL(), SeedFile: seed, Names: natsmcp.Names{Group: "dev", Me: "beta"},
		PushFrom: map[string]bool{}, MaxText: natsmcp.DefaultMaxText, InboxMax: 50, InboxPerMan: 50, Quiet: true}, quietLog{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := beta.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(beta.Close)
	h.OpenTunnel = func(context.Context, driving.NATSTarget, string, int) (string, func(), error) {
		return strings.TrimPrefix(srv.ClientURL(), "nats://"), func() {}, nil
	}
	return &chatWorld{opts: &cli.Opts{Host: h, Dir: dir}, env: env, beta: beta}
}

func (w *chatWorld) tool(t *testing.T, name, args string) string {
	t.Helper()
	for _, tl := range w.beta.Tools() {
		if tl.Name == name {
			txt, err := tl.Run(context.Background(), []byte(args))
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			return txt
		}
	}
	t.Fatalf("no tool %s", name)
	return ""
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(8 * time.Second); time.Now().Before(end); time.Sleep(30 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("never: %s", what)
}

func TestTheChatSendsAMessageAndABroadcastAsTheOperator(t *testing.T) {
	w := newChatWorld(t)
	b := cli.ChatBackend(w.opts, w.env)
	w.tool(t, "subscribe", `{"topic":"news"}`)
	text := "line one\n```\n$(id) 'quoted' \"double\"\n```"
	if _, err := b.Send(context.Background(), "dev", chatweb.Outgoing{Kind: "msg", To: "beta", Text: text}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Send(context.Background(), "dev", chatweb.Outgoing{Kind: "topic", Topic: "news", Text: "to all"}, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "both in the inbox", func() bool {
		in := w.tool(t, "inbox", "{}")
		return strings.Contains(in, "$(id) 'quoted'") && strings.Contains(in, "to all")
	})
	if _, err := b.Send(context.Background(), "dev", chatweb.Outgoing{Kind: "msg", To: "nobody", Text: "x"}, nil); err == nil {
		t.Error("a machine that is not in the group")
	}
}

func TestTheChatAsksAndTheWaitingEndsWithTheAnswerOrWithoutOne(t *testing.T) {
	w := newChatWorld(t)
	b := cli.ChatBackend(w.opts, w.env)
	type result struct {
		id  string
		err error
	}
	done := make(chan result, 1)
	sent, err := b.Send(context.Background(), "dev", chatweb.Outgoing{Kind: "ask", To: "beta", Text: "ping", Timeout: 20 * time.Second}, func(id string, err error) { done <- result{id, err} })
	id := sent.ID
	if err != nil || len(id) < 16 {
		t.Fatalf("%q %v", id, err)
	}
	waitFor(t, "the question is there", func() bool { return strings.Contains(w.tool(t, "inbox", "{}"), "ping") })
	w.tool(t, "reply", `{"handle":1,"text":"pong"}`) // the first thing beta got
	select {
	case r := <-done:
		if r.err != nil || r.id != id {
			t.Errorf("%+v", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting does not end with the answer")
	}
	_, err = b.Send(context.Background(), "dev", chatweb.Outgoing{Kind: "ask", To: "beta", Text: "anybody?", Timeout: time.Second}, func(_ string, err error) { done <- result{err: err} })
	if err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err == nil || !strings.Contains(r.err.Error(), "no answer") {
			t.Errorf("%+v", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting does not end")
	}
}

func TestTheChatReadsTheStreamAndAnswersAQuestionOfAMachine(t *testing.T) {
	w := newChatWorld(t)
	b := cli.ChatBackend(w.opts, w.env)
	conn, err := cli.ChatConnector(w.opts, w.env).Connect(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var out syncBuf
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = conn.Watch(ctx, &out, &syncBuf{}) }()
	waitFor(t, "the stream is ready", func() bool { return strings.Contains(out.String(), `"ready"`) })
	got := make(chan string, 1)
	go func() {
		e, err := w.beta.Ask(context.Background(), "operator", "can I?", 20*time.Second)
		if err != nil {
			got <- err.Error()
			return
		}
		got <- e.Text
	}()
	var id string
	waitFor(t, "the question is in the stream", func() bool {
		for _, l := range strings.Split(out.String(), "\n") {
			var x model.NATSLine
			if json.Unmarshal([]byte(l), &x) == nil && x.Type == "req" && x.From == "beta" {
				id = x.ID
			}
		}
		return id != ""
	})
	if _, err := b.Send(context.Background(), "dev", chatweb.Outgoing{Kind: "reply", To: "beta", Re: id, Text: "yes"}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		if s != "yes" {
			t.Errorf("the answer is %q", s)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("beta got no answer")
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }
