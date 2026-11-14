package natsserver_test

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"github.com/pt9912/hetzner-viper/internal/natsserver"
)

// These tests run a real nats-server (as a library) with the permissions Render writes, and try to
// publish where a machine must not. Only the listen address and TLS differ from production.

type forgeClient struct {
	nc   *nats.Conn
	mu   sync.Mutex
	errs []string
}

func (c *forgeClient) violations() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.errs, "; ")
}

func forgeSetup(t *testing.T) (a, b, c *forgeClient) {
	t.Helper()
	seeds := map[string]string{}
	var clients []natsserver.Client
	for _, m := range []string{"a", "b", "c"} {
		id, err := natsserver.NewIdentity()
		if err != nil {
			t.Fatal(err)
		}
		seeds[m] = id.Seed
		clients = append(clients, natsserver.Client{Machine: m, PublicKey: id.PublicKey})
	}
	block, err := natsserver.AuthorizationBlock("g", clients, false)
	if err != nil {
		t.Fatal(err)
	}
	// production permissions behind a loopback listener without TLS
	conf := "host: \"127.0.0.1\"\nport: -1\n\n" + block
	path := t.TempDir() + "/server.conf"
	if err := writeFile(path, conf); err != nil {
		t.Fatal(err)
	}
	opts, err := server.ProcessConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(5 * time.Second) {
		t.Fatal("server not ready")
	}
	t.Cleanup(s.Shutdown)
	connect := func(m string) *forgeClient {
		kp, err := nkeys.FromSeed([]byte(seeds[m]))
		if err != nil {
			t.Fatal(err)
		}
		pub, _ := kp.PublicKey()
		fc := &forgeClient{}
		nc, err := nats.Connect(s.ClientURL(), nats.Nkey(pub, kp.Sign), nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			fc.mu.Lock()
			fc.errs = append(fc.errs, err.Error())
			fc.mu.Unlock()
		}))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(nc.Close)
		fc.nc = nc
		return fc
	}
	return connect("a"), connect("b"), connect("c")
}

func listen(t *testing.T, c *forgeClient, subject string) chan *nats.Msg {
	t.Helper()
	ch := make(chan *nats.Msg, 8)
	if _, err := c.nc.ChanSubscribe(subject, ch); err != nil {
		t.Fatal(err)
	}
	if err := c.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	return ch
}

func arrives(ch chan *nats.Msg) *nats.Msg {
	select {
	case m := <-ch:
		return m
	case <-time.After(600 * time.Millisecond):
		return nil
	}
}

func TestAMachineCannotPublishOutsideItsOwnAreaEvenThroughReplies(t *testing.T) {
	a, b, c := forgeSetup(t)

	t.Run("direct publish into another machine's area is refused", func(t *testing.T) {
		victim := listen(t, b, "viper.g.*.out.b")
		_ = c.nc.Publish("viper.g.a.out.b", []byte("forged"))
		_ = c.nc.Flush()
		if arrives(victim) != nil || !strings.Contains(c.violations(), `Publish to "viper.g.a.out.b"`) {
			t.Errorf("must be refused: %q", c.violations())
		}
	})

	t.Run("a machine cannot answer its own message on a reply subject of its choice", func(t *testing.T) {
		own := listen(t, c, "viper.g.c.x")
		victim := listen(t, b, "viper.g.a.out.b")
		_ = c.nc.PublishMsg(&nats.Msg{Subject: "viper.g.c.x", Reply: "viper.g.a.out.b", Data: []byte("hello")})
		_ = c.nc.Flush()
		mine := arrives(own)
		if mine == nil {
			t.Fatal("the machine reads its own area, so it gets its own message")
		}
		_ = c.nc.Publish(mine.Reply, []byte("FORGED"))
		_ = c.nc.Flush()
		if m := arrives(victim); m != nil {
			t.Errorf("a message of c arrived in a's area: %q", m.Data)
		}
	})

	t.Run("a third party cannot answer somebody else's request", func(t *testing.T) {
		inbox := listen(t, a, "_INBOX.a.>")
		seen := listen(t, c, "viper.g.a.out.b")
		_ = a.nc.PublishMsg(&nats.Msg{Subject: "viper.g.a.out.b", Reply: "_INBOX.a.1", Data: []byte("question")})
		_ = a.nc.Flush()
		q := arrives(seen)
		if q == nil {
			t.Fatal("c reads the whole group")
		}
		_ = c.nc.Publish(q.Reply, []byte("ANSWER-by-c"))
		_ = c.nc.Flush()
		if m := arrives(inbox); m != nil {
			t.Errorf("a received an answer from c: %q", m.Data)
		}
	})

	t.Run("a responder cannot be made to publish into another machine's area", func(t *testing.T) {
		forged := listen(t, c, "viper.g.a.out.c")
		req := listen(t, b, "viper.g.c.req.b")
		_ = c.nc.PublishMsg(&nats.Msg{Subject: "viper.g.c.req.b", Reply: "viper.g.a.out.c", Data: []byte("please answer")})
		_ = c.nc.Flush()
		r := arrives(req)
		if r == nil {
			t.Fatal("the request subtree is open to every machine")
		}
		_ = b.nc.Publish(r.Reply, []byte("reply-by-b"))
		_ = b.nc.Flush()
		if m := arrives(forged); m != nil {
			t.Errorf("b published into a's area: %q", m.Data)
		}
	})
}

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o600) }
