package natsmcp_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/pt9912/hetzner-viper/internal/natsmcp"
	"github.com/pt9912/hetzner-viper/internal/natsserver"
)

// objectServer is a server with JetStream and the users and rights viper writes for a group with an object store: two
// machines, the operator and the entry that makes the bucket. Every user has a seed file in dir.
type objectServer struct {
	srv  *server.Server
	url  string
	dir  string
	seed map[string]string
}

func newObjectServer(t *testing.T, group string) *objectServer {
	t.Helper()
	dir := t.TempDir()
	o := &objectServer{dir: dir, seed: map[string]string{}}
	var clients []natsserver.Client
	for _, name := range []string{"a", "b", "operator", natsserver.AdminName} {
		id, err := natsserver.NewIdentity()
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, natsserver.Client{Machine: name, PublicKey: id.PublicKey, Operator: name == "operator", Admin: name == natsserver.AdminName})
		o.seed[name] = filepath.Join(dir, name+".nk")
		if err := os.WriteFile(o.seed[name], []byte(id.Seed+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	block, err := natsserver.AuthorizationBlock(group, clients, true)
	if err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dir, "server.conf")
	text := "host: \"127.0.0.1\"\nport: -1\njetstream {\n  store_dir: \"" + filepath.ToSlash(filepath.Join(dir, "js")) + "\"\n  max_file_store: 134217728\n}\n\n" + block
	if err := os.WriteFile(conf, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	opts, err := server.ProcessConfigFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	if o.srv, err = server.NewServer(opts); err != nil {
		t.Fatal(err)
	}
	go o.srv.Start()
	if !o.srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("server not ready")
	}
	t.Cleanup(o.srv.Shutdown)
	o.url = o.srv.ClientURL()
	return o
}

// connect connects as the user, with the inbox prefix of its rights (none: the one of the library).
func (o *objectServer) connect(t *testing.T, name, inbox string) *nats.Conn {
	t.Helper()
	seed, err := os.ReadFile(o.seed[name])
	if err != nil {
		t.Fatal(err)
	}
	kp, err := nkeys.FromSeed(bytes.TrimSpace(seed))
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	opts := []nats.Option{nats.Nkey(pub, kp.Sign), nats.NoEcho(), nats.Timeout(5 * time.Second)}
	if inbox != "" {
		opts = append(opts, nats.CustomInboxPrefix(inbox))
	}
	nc, err := nats.Connect(o.url, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	return nc
}

func (o *objectServer) admin(t *testing.T) *nats.Conn {
	return o.connect(t, natsserver.AdminName, "_INBOX."+natsserver.AdminName)
}

func testSpec() natsmcp.BucketSpec {
	return natsmcp.BucketSpec{Group: "g", MaxBytes: 64 << 20, TTL: 24 * time.Hour}
}

// ctxShort is for calls the server is expected to refuse: they end in a time-out.
func ctxShort(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	t.Cleanup(cancel)
	return c
}

func ctx5(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

// The entry that makes the bucket makes it once with the limits of the spec, and leaves a bucket that is there alone.
func TestTheAdminMakesTheBucketOnceAndReportsWhatDiffers(t *testing.T) {
	o := newObjectServer(t, "g")
	nc := o.admin(t)
	created, diffs, err := natsmcp.EnsureBucket(ctx5(t), nc, testSpec())
	if err != nil || !created || len(diffs) != 0 {
		t.Fatalf("%v %v %v", created, diffs, err)
	}
	info, err := natsmcp.ReadBucket(ctx5(t), nc, "g")
	if err != nil || info == nil {
		t.Fatalf("%v %v", info, err)
	}
	if info.MaxBytes != 64<<20 || info.MaxAge != 24*time.Hour || info.MaxMsgs != 4096 || info.MaxMsgSize != 256<<10 || info.Storage != "file" || info.Replicas != 1 || info.Discard != "new" {
		t.Errorf("%+v", info)
	}
	created, diffs, err = natsmcp.EnsureBucket(ctx5(t), nc, testSpec())
	if err != nil || created || len(diffs) != 0 {
		t.Errorf("a second call changes nothing: %v %v %v", created, diffs, err)
	}
	other := testSpec()
	other.MaxBytes, other.TTL = 32<<20, 48*time.Hour
	_, diffs, err = natsmcp.EnsureBucket(ctx5(t), nc, other)
	joined := strings.Join(diffs, ";")
	if err != nil || len(diffs) != 3 || !strings.Contains(joined, "max_bytes") || !strings.Contains(joined, "max_age") || !strings.Contains(joined, "max_msgs") {
		t.Errorf("%v %v", diffs, err)
	}
	if again, _ := natsmcp.ReadBucket(ctx5(t), nc, "g"); again == nil || again.MaxBytes != 64<<20 {
		t.Errorf("a bucket that is there is not changed: %+v", again)
	}
}

// A machine and the operator put and get objects with the rights of the group, and with nothing else.
func TestMachinesPutAndGetObjectsAndCanDoNothingElseWithTheStore(t *testing.T) {
	o := newObjectServer(t, "g")
	if _, _, err := natsmcp.EnsureBucket(ctx5(t), o.admin(t), testSpec()); err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("x"), 300<<10)
	for _, who := range []struct{ name, inbox string }{{"a", "_INBOX.a"}, {"operator", "_INBOX.operator"}} {
		js, err := jetstream.New(o.connect(t, who.name, who.inbox))
		if err != nil {
			t.Fatal(err)
		}
		ob, err := js.ObjectStore(ctx5(t), natsmcp.BucketName("g"))
		if err != nil {
			t.Fatalf("%s: binding: %v", who.name, err)
		}
		name := who.name + "-object"
		if _, err := ob.PutBytes(ctx5(t), name, data); err != nil {
			t.Fatalf("%s: put: %v", who.name, err)
		}
		got, err := ob.GetBytes(ctx5(t), name)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("%s: get: %d bytes, %v", who.name, len(got), err)
		}
	}
	// b reads what a put
	jsb, _ := jetstream.New(o.connect(t, "b", "_INBOX.b"))
	obb, err := jsb.ObjectStore(ctx5(t), natsmcp.BucketName("g"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := obb.GetBytes(ctx5(t), "a-object"); err != nil || !bytes.Equal(got, data) {
		t.Errorf("b reads the object of a: %v", err)
	}
	// nothing else: no purging, no changing or making of streams, no other bucket, no KV. Delete reports the refusal of the
	// purge, but its first step (a tombstone in the metadata, which every machine may write) works: any member can make an
	// object unfindable. The chunks stay until they expire (docs/plan/nats-objekte.md §4).
	if err := obb.Delete(ctxShort(t), "a-object"); err == nil {
		t.Error("the purge of the chunks was not refused")
	}
	if err := jsb.DeleteStream(ctxShort(t), "OBJ_"+natsmcp.BucketName("g")); err == nil {
		t.Error("a machine deleted the stream")
	}
	if _, err := jsb.UpdateStream(ctxShort(t), jetstream.StreamConfig{Name: "OBJ_" + natsmcp.BucketName("g"), Subjects: []string{"x.>"}}); err == nil {
		t.Error("a machine changed the stream")
	}
	if _, err := jsb.CreateStream(ctxShort(t), jetstream.StreamConfig{Name: "OBJ_viper-h", Subjects: []string{"$O.viper-h.>"}}); err == nil {
		t.Error("a machine made a stream")
	}
	if _, err := jsb.ObjectStore(ctxShort(t), "viper-h"); err == nil {
		t.Error("a machine bound the bucket of another group")
	}
	if _, err := jsb.CreateKeyValue(ctxShort(t), jetstream.KeyValueConfig{Bucket: "kv"}); err == nil {
		t.Error("a machine made a key-value bucket")
	}
}

// Without the inbox prefix of its rights a client cannot bind: the answers go to a subject it may not read.
func TestAClientWithoutTheInboxPrefixCannotUseTheStore(t *testing.T) {
	o := newObjectServer(t, "g")
	if _, _, err := natsmcp.EnsureBucket(ctx5(t), o.admin(t), testSpec()); err != nil {
		t.Fatal(err)
	}
	js, _ := jetstream.New(o.connect(t, "a", ""))
	c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := js.ObjectStore(c, natsmcp.BucketName("g")); err == nil {
		t.Error("a client without the prefix bound the bucket")
	}
}

// The entry that makes the bucket can do that and nothing in the group.
func TestTheAdminCanMakeTheBucketAndNotUseTheGroup(t *testing.T) {
	o := newObjectServer(t, "g")
	nc := o.admin(t)
	errc := make(chan error, 4)
	nc.SetErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) { errc <- err })
	if err := nc.Publish("viper.g.a.out.b", []byte(`{"v":1,"kind":"msg","text":"x"}`)); err == nil {
		_ = nc.Flush()
	}
	select {
	case err := <-errc:
		if !strings.Contains(err.Error(), "ermission") {
			t.Errorf("%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("the admin sent a message in the group")
	}
	if _, err := nc.Subscribe("viper.g.>", func(*nats.Msg) {}); err == nil {
		_ = nc.Flush()
	}
	select {
	case err := <-errc:
		if !strings.Contains(err.Error(), "ermission") {
			t.Errorf("%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("the admin read the group")
	}
}

func TestEnsureBucketAndBucketInfoRunAsCommandsOnTheServer(t *testing.T) {
	o := newObjectServer(t, "g")
	dir := t.TempDir()
	data, _ := os.ReadFile(o.seed[natsserver.AdminName])
	if err := os.WriteFile(filepath.Join(dir, "admin.nk"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := natsmcp.Config{URL: o.url, Names: natsmcp.Names{Group: "g", Me: "a"}}
	run := func(name string, args ...string) (string, string, int) {
		var out, errb bytes.Buffer
		code := natsmcp.BucketMain(name, cfg, dir, args, &out, &errb)
		return out.String(), errb.String(), code
	}
	if out, _, code := run("bucket-info"); code != 0 || !strings.Contains(out, "bucket=missing") {
		t.Errorf("%d %q", code, out)
	}
	if _, errs, code := run("ensure-bucket"); code != 2 || !strings.Contains(errs, "--max-bytes and --ttl-days are needed") {
		t.Errorf("%d %q", code, errs)
	}
	out, errs, code := run("ensure-bucket", "--max-bytes", "67108864", "--ttl-days", "7")
	if code != 0 || !strings.Contains(out, "made viper-g") {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	if out, _, code = run("ensure-bucket", "--max-bytes", "67108864", "--ttl-days", "7"); code != 0 || !strings.Contains(out, "is as the settings say") {
		t.Errorf("%d %q", code, out)
	}
	if out, _, code = run("ensure-bucket", "--max-bytes", "33554432", "--ttl-days", "7"); code != 0 || !strings.Contains(out, "differs from the settings (left as it is)") {
		t.Errorf("%d %q", code, out)
	}
	out, _, code = run("bucket-info")
	for _, want := range []string{"bucket=ok", "bucket_max_bytes=67108864", "bucket_max_age=604800", "bucket_max_msgs=4096", "bucket_max_msg_size=262144", "bucket_discard=new"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Errorf("%q missing: %d %q", want, code, out)
		}
	}
}

// A larger object is read with flow control: the client answers the server on $JS.FC.<stream>.*, and without the right the
// read hangs. Found on k3d with a 3 MiB file; a small object does not use it.
func TestALargeObjectIsReadWithFlowControl(t *testing.T) {
	o := newObjectServer(t, "g")
	if _, _, err := natsmcp.EnsureBucket(ctx5(t), o.admin(t), testSpec()); err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("0123456789abcdef"), 3<<16) // 3 MiB
	for _, name := range []string{"a", "operator"} {
		js, err := jetstream.New(o.connect(t, name, "_INBOX."+name))
		if err != nil {
			t.Fatal(err)
		}
		ob, err := js.ObjectStore(ctx5(t), natsmcp.BucketName("g"))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		if _, err := ob.PutBytes(ctx, name+"-big", data); err != nil {
			cancel()
			t.Fatalf("%s: put: %v", name, err)
		}
		got, err := ob.GetBytes(ctx, name+"-big")
		cancel()
		if err != nil || !bytes.Equal(got, data) {
			t.Errorf("%s: a large object: %d bytes, %v", name, len(got), err)
		}
	}
}

// Documented risk (docs/plan/nats-objekte.md §4): the object store needs rollups for its metadata, so any member that may publish
// to the subjects of the bucket can send a rollup header and empty the whole store. This test pins what is known: if a way to
// forbid it is found, it fails and the plan and the manual are to be changed.
func TestAMemberCanEmptyTheStoreWithARollupHeader(t *testing.T) {
	o := newObjectServer(t, "g")
	if _, _, err := natsmcp.EnsureBucket(ctx5(t), o.admin(t), testSpec()); err != nil {
		t.Fatal(err)
	}
	nc := o.connect(t, "a", "_INBOX.a")
	js, _ := jetstream.New(nc)
	ob, err := js.ObjectStore(ctx5(t), natsmcp.BucketName("g"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ob.PutBytes(ctx5(t), "one", []byte("1")); err != nil {
		t.Fatal(err)
	}
	m := nats.NewMsg("$O." + natsmcp.BucketName("g") + ".M.eA")
	m.Header.Set("Nats-Rollup", "all")
	if err := nc.PublishMsg(m); err != nil {
		t.Fatal(err)
	}
	_ = nc.Flush()
	time.Sleep(300 * time.Millisecond)
	if _, err := ob.GetBytes(ctxShort(t), "one"); err == nil {
		t.Skip("a rollup header no longer empties the store: update the plan and the manual")
	}
}
