package cli_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/natsmcp"
	"github.com/pt9912/hetzner-viper/internal/natsserver"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

// viper-nats-mcp --via-viper starts viper nats tunnel as a child. The tests start the test program itself as that child ("viper") and as the
// MCP server: TestMain runs it as one of them when VIPER_TEST_AS says so (the child gets only HOME, PATH and the variables VIPER_*).

const (
	asViper = "viper"
	asMCP   = "mcp"
	testVer = "test"
)

func TestMain(m *testing.M) {
	switch os.Getenv("VIPER_TEST_AS") {
	case asViper:
		os.Exit(viperAsChild())
	case asMCP: // both programs of a pair: the server is started with --via-viper, the viper it starts is not
		if len(os.Args) > 1 && os.Args[1] == "--via-viper" {
			os.Exit(natsmcp.Main(testVer, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) //nolint:forbidigo // this process plays the program: its streams are the real ones
		}
		os.Exit(viperAsChild())
	}
	os.Exit(m.Run())
}

// viperAsChild is viper with the server of the test behind its tunnel: the settings and keys are real, the registration at the server and
// the ssh tunnel are answered from VIPER_TEST_PUB and VIPER_TEST_ADDR.
func viperAsChild() int {
	h := testHost()
	h.LookPath = func(string) (string, error) { return "/usr/bin/tool", nil }
	h.Stdin, h.Stdout, h.Stderr = os.Stdin, os.Stdout, os.Stderr //nolint:forbidigo // this process plays viper: its streams are the real ones
	h.Services.NATSOp = &fakeOp{registered: map[string]string{os.Getenv("VIPER_TEST_NAME"): os.Getenv("VIPER_TEST_PUB")}}
	h.OpenTunnel = func(context.Context, driving.NATSTarget, string, int) (string, func(), error) {
		return os.Getenv("VIPER_TEST_ADDR"), func() {}, nil
	}
	return cli.Main(testVer, os.Args[1:], h)
}

// viaWorld is the group of chatWorld with the identity local made for the tunnel: its key where viper nats init puts it.
type viaWorld struct {
	*chatWorld
	viper string // the program that plays viper
	dir   string
}

func newViaWorld(t *testing.T) *viaWorld {
	t.Helper()
	w := newChatWorld(t)
	state := model.StateDir(w.env.Home)
	key := model.IdentityKeyFile(state, "dev", "local")
	seed, err := os.ReadFile(w.localSeed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	pub, err := natsserver.PublicKeyOf(string(seed))
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", w.env.Home)
	t.Setenv("USERPROFILE", w.env.Home)
	t.Setenv("VIPER_TEST_NAME", "local")
	t.Setenv("VIPER_TEST_PUB", pub)
	t.Setenv("VIPER_TEST_ADDR", strings.TrimPrefix(w.url, "nats://"))
	dir, _ := filepath.Abs(w.opts.Dir)
	return &viaWorld{chatWorld: w, viper: exe, dir: dir}
}

// viaRig runs the server in this process, with the test program as the child viper.
func (v *viaWorld) viaRig(t *testing.T, extra ...string) (*mcpRig, *lockedBuf) {
	t.Helper()
	errs := &lockedBuf{}
	args := append([]string{"dev", "--viper", v.viper, "--dir", v.dir}, extra...)
	t.Setenv("VIPER_TEST_AS", asViper) // for the child; the server in this process does not read it
	r := startRig(t, errs.String, func(_ context.Context, in io.Reader, out io.Writer) {
		natsmcp.ViaViperMain(testVer, args, in, out, errs) //nolint:contextcheck // the program entry makes its own context
	})
	return r, errs
}

func TestViaViperStartsTheTunnelAndTheToolsWork(t *testing.T) {
	v := newViaWorld(t)
	r, errs := v.viaRig(t)
	if text, isErr := r.call("send", map[string]any{"to": "beta", "text": "hello over the tunnel"}); isErr {
		t.Fatalf("send: %s\n%s", text, errs.String())
	}
	waitFor(t, "beta has it, from local", func() bool {
		in := v.tool(t, "inbox", "{}")
		return strings.Contains(in, "hello over the tunnel") && strings.Contains(in, "local")
	})
	if !strings.Contains(errs.String(), "viper nats tunnel pid") {
		t.Errorf("the log names the child: %q", errs.String())
	}
}

func TestDelegateAsksForTheConfirmationAndTheResultComesAsAMessage(t *testing.T) {
	v := newViaWorld(t)
	r, _ := v.viaRig(t)
	go func() {
		waitFor(t, "beta has the job", func() bool { return strings.Contains(v.tool(t, "inbox", "{}"), "build the thing") })
		v.tool(t, "reply", `{"handle":1,"text":"accepted, the result follows"}`)
	}()
	text, isErr := r.call("delegate", map[string]any{"to": "beta", "text": "build the thing", "timeout_seconds": 20})
	if isErr || !strings.Contains(text, "confirmed by beta") || !strings.Contains(text, "accepted, the result follows") {
		t.Fatalf("delegate: %v %q", isErr, text)
	}
	in := v.tool(t, "inbox", "{}")
	for _, want := range []string{"[Job from local]", "send the result with send to local", "build the thing"} {
		if !strings.Contains(in, want) {
			t.Errorf("the job lacks %q:\n%s", want, in)
		}
	}
	if err := v.beta.SendText("local", "the thing is built"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the result is in the inbox of local", func() bool {
		got, _ := r.call("inbox", map[string]any{})
		return strings.Contains(got, "the thing is built")
	})
	// nobody confirms
	text, isErr = r.call("delegate", map[string]any{"to": "gamma", "text": "another job", "timeout_seconds": 1})
	if isErr || !strings.Contains(text, "not confirmed") {
		t.Errorf("an unconfirmed job says so: %v %q", isErr, text)
	}
}

func TestSendPerMinStopsAnAgentThatWritesTooMuch(t *testing.T) {
	v := newViaWorld(t)
	r, _ := v.viaRig(t, "--send-per-min", "2")
	for i := 0; i < 2; i++ {
		if text, isErr := r.call("send", map[string]any{"to": "beta", "text": "n" + strconv.Itoa(i)}); isErr {
			t.Fatalf("send %d: %s", i, text)
		}
	}
	if text, isErr := r.call("send", map[string]any{"to": "beta", "text": "one too many"}); !isErr || !strings.Contains(text, "too many") {
		t.Errorf("the third call: %v %q", isErr, text)
	}
	if _, isErr := r.call("inbox", map[string]any{}); isErr {
		t.Error("tools that do not write are not counted")
	}
}

func TestViaViperTellsTheSessionOfAComputerWhatItIs(t *testing.T) {
	v := newViaWorld(t)
	r, _ := v.viaRig(t)
	r.send(`{"jsonrpc":"2.0","id":99,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	m := r.await(func(m map[string]any) bool { return m["id"] == float64(99) })
	res, _ := m["result"].(map[string]any)
	if ins, _ := res["instructions"].(string); !strings.Contains(ins, "own computer") || !strings.Contains(ins, "delegate") {
		t.Errorf("instructions: %q", ins)
	}
}
