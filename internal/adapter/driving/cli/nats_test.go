package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/settingsfile"
	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

// natsGroup is a group dev with alpha, beta and gamma that have run viper up (an SSH config each).
func natsGroup(t *testing.T, extra string) (string, settings.Env) {
	t.Helper()
	dir := upGroupDir(t, "alpha,beta,gamma", "NATS_MCP=true\n"+extra)
	env := upEnv(t)
	for _, m := range []string{"alpha", "beta", "gamma"} {
		c, err := settingsfile.Load(dir, m, env, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(c.SSHConfig), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(c.SSHConfig, []byte("Host x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, env
}

// ssh records the calls of ssh and answers with lines.
type natsSSH struct {
	mu    sync.Mutex
	calls [][]string
	stdin []string
	out   string
	err   error
}

func (s *natsSSH) stream(_ context.Context, stdin string, stdout, _ io.Writer, _ string, args ...string) error {
	s.mu.Lock()
	s.calls = append(s.calls, args)
	s.stdin = append(s.stdin, stdin)
	s.mu.Unlock()
	_, _ = io.WriteString(stdout, s.out)
	return s.err
}

func runNats(t *testing.T, dir string, env settings.Env, ssh *natsSSH, in string, p cli.Opts) (stdout, stderr string, err error) {
	t.Helper()
	_, h := fakeTools(t)
	h.StreamTool = ssh.stream
	h.Env = func() (settings.Env, error) { return env, nil }
	h.Stdin = strings.NewReader(in)
	var out, errb bytes.Buffer
	h.Stdout, h.Stderr = &out, &errb
	p.Host, p.Dir = h, dir
	err = cli.RunCommand("nats", &p)
	return out.String(), errb.String(), err
}

func TestNatsSendRunsTheProgramOfTheMachineWithTheTextOnStdin(t *testing.T) {
	dir, env := natsGroup(t, "")
	ssh := &natsSSH{out: `{"event":"sent"}` + "\n"}
	text := "line1\n'quoted' $(id) {{Random 5 5}}"
	out, errs, err := runNats(t, dir, env, ssh, "", cli.Opts{Args: []string{"send", "dev", text}, To: "beta"})
	if err != nil {
		t.Fatalf("%v\n%s", err, errs)
	}
	if len(ssh.calls) != 1 || ssh.stdin[0] != text {
		t.Fatalf("calls %v stdin %q", ssh.calls, ssh.stdin)
	}
	last := ssh.calls[0][len(ssh.calls[0])-1]
	if !strings.Contains(last, "viper-nats-mcp' 'send' '--to' 'beta'") {
		t.Errorf("remote command %q", last)
	}
	if strings.Contains(last, "quoted") || strings.Contains(last, "$(id)") {
		t.Errorf("the text is part of the command line: %q", last)
	}
	for _, a := range ssh.calls[0] {
		if a == "-n" {
			t.Error("-n closes stdin, the text could not be sent")
		}
	}
	if !strings.Contains(out, "sent as alpha to beta") || !strings.Contains(errs, "Sender: alpha") {
		t.Errorf("stdout %q stderr %q", out, errs)
	}
}

func TestNatsTheSenderIsNeverTheReceiverAndFromChoosesIt(t *testing.T) {
	dir, env := natsGroup(t, "")
	ssh := &natsSSH{out: `{"event":"sent"}` + "\n"}
	if _, errs, err := runNats(t, dir, env, ssh, "", cli.Opts{Args: []string{"send", "dev", "x"}, To: "alpha"}); err != nil || !strings.Contains(errs, "Sender: beta") {
		t.Errorf("%v %q", err, errs)
	}
	if _, errs, err := runNats(t, dir, env, ssh, "", cli.Opts{Args: []string{"send", "dev", "x"}, To: "alpha", From: "gamma"}); err != nil || !strings.Contains(errs, "Sender: gamma") {
		t.Errorf("%v %q", err, errs)
	}
	if _, _, err := runNats(t, dir, env, ssh, "", cli.Opts{Args: []string{"send", "dev", "x"}, To: "alpha", From: "alpha"}); err == nil {
		t.Error("a machine cannot send to itself")
	}
	if _, _, err := runNats(t, dir, env, ssh, "", cli.Opts{Args: []string{"send", "dev", "x"}, To: "beta", From: "delta"}); err == nil {
		t.Error("--from names a machine outside the group")
	}
	if _, _, err := runNats(t, dir, env, ssh, "", cli.Opts{Args: []string{"send", "dev", "x"}, To: "delta"}); err == nil {
		t.Error("--to names a machine outside the group")
	}
}

func TestNatsRefusesWhatWouldBePushedAsTheMachineWithoutYes(t *testing.T) {
	dir, env := natsGroup(t, "NATS_MCP_PUSH_FROM=alpha\n")
	ssh := &natsSSH{out: `{"event":"sent"}` + "\n"}
	_, _, err := runNats(t, dir, env, ssh, "", cli.Opts{Args: []string{"send", "dev", "x"}, To: "beta"})
	if err == nil || !strings.Contains(err.Error(), "NATS_MCP_PUSH_FROM") || len(ssh.calls) != 0 {
		t.Fatalf("%v, calls %d", err, len(ssh.calls))
	}
	_, errs, err := runNats(t, dir, env, ssh, "", cli.Opts{Args: []string{"send", "dev", "x"}, To: "beta", Yes: true})
	if err != nil || !strings.Contains(errs, "Note: alpha is in NATS_MCP_PUSH_FROM") {
		t.Errorf("%v %q", err, errs)
	}
	// another sender, or only reading, needs no yes
	if _, _, err := runNats(t, dir, env, ssh, "", cli.Opts{Args: []string{"send", "dev", "x"}, To: "alpha"}); err != nil {
		t.Errorf("beta is on nobody's list: %v", err)
	}
	if _, _, err := runNats(t, dir, env, ssh, "", cli.Opts{Args: []string{"peers", "dev"}}); err != nil {
		t.Errorf("reading is not a message in the name of the machine: %v", err)
	}
}

func TestNatsTextFromStdinAndOptionChecks(t *testing.T) {
	dir, env := natsGroup(t, "")
	ssh := &natsSSH{out: `{"event":"sent"}` + "\n"}
	if _, _, err := runNats(t, dir, env, ssh, "from stdin\n", cli.Opts{Args: []string{"broadcast", "dev", "-"}, Topic: "news"}); err != nil || ssh.stdin[0] != "from stdin" {
		t.Errorf("%v %q", err, ssh.stdin)
	}
	for _, p := range []cli.Opts{
		{Args: []string{"send", "dev"}, To: "beta"},                          // no text
		{Args: []string{"send", "dev", "x"}},                                 // no --to
		{Args: []string{"broadcast", "dev", "x"}},                            // no --topic
		{Args: []string{"broadcast", "dev", "x"}, Topic: "news", To: "beta"}, // --to is not used
		{Args: []string{"peers", "dev", "x"}},                                // text where none belongs
		{Args: []string{"listen", "dev"}, Query: true},                       // --query is not used
		{Args: []string{"dance", "dev"}},                                     // unknown
		{Args: []string{"peers"}},                                            // no group
		{Args: []string{"send", "dev", "x"}, To: "be.ta"},                    // not a name
		{Args: []string{"broadcast", "dev", "x"}, Topic: "a b"},              //
	} {
		before := len(ssh.calls)
		if _, _, err := runNats(t, dir, env, ssh, "", p); err == nil || len(ssh.calls) != before {
			t.Errorf("%+v was accepted (%v)", p, err)
		}
	}
}

func TestNatsPeersAndRequestAndNoMachineThatCanSpeak(t *testing.T) {
	dir, env := natsGroup(t, "")
	ssh := &natsSSH{out: `{"event":"ready","me":"alpha","group":"dev"}
{"event":"message","from":"beta","via":"presence","type":"presence","state":"up"}
`}
	out, _, err := runNats(t, dir, env, ssh, "", cli.Opts{Args: []string{"peers", "dev"}})
	if err != nil || !strings.Contains(out, "- beta") || strings.Contains(out, "gamma") {
		t.Errorf("%v %q", err, out)
	}
	if last := ssh.calls[0]; last[0] != "-n" { // no text: stdin is closed
		t.Errorf("%v", last)
	}
	ssh = &natsSSH{out: `{"event":"answer","from":"beta","text":"pong"}` + "\n"}
	out, _, err = runNats(t, dir, env, ssh, "", cli.Opts{Args: []string{"request", "dev", "ping"}, To: "beta"})
	if err != nil || strings.TrimSpace(out) != "pong" {
		t.Errorf("%v %q", err, out)
	}
	ssh = &natsSSH{err: errors.New("exit status 1")}
	if _, _, err := runNats(t, dir, env, ssh, "", cli.Opts{Args: []string{"request", "dev", "ping"}, To: "beta"}); cli.ExitCode(err) == 0 {
		t.Errorf("an error of the machine was lost: %v", err)
	}
	// no machine has run viper up
	bare := upGroupDir(t, "x1,x2", "NATS_MCP=true\n")
	if _, _, err := runNats(t, bare, env, &natsSSH{}, "", cli.Opts{Args: []string{"peers", "dev"}}); err == nil || !strings.Contains(err.Error(), "no machine of the group can speak") {
		t.Errorf("%v", err)
	}
}

func TestNatsJSONOutput(t *testing.T) {
	dir, env := natsGroup(t, "")
	ssh := &natsSSH{out: `{"event":"message","from":"beta","via":"pub","topic":"news","type":"msg","text":"hi"}` + "\n"}
	out, _, err := runNats(t, dir, env, ssh, "", cli.Opts{Args: []string{"listen", "dev"}, JSON: true})
	if err != nil || !strings.Contains(out, `"from":"beta"`) || !strings.Contains(out, `"topic":"news"`) {
		t.Errorf("%v %q", err, out)
	}
	out, _, err = runNats(t, dir, env, ssh, "", cli.Opts{Args: []string{"listen", "dev"}})
	if err != nil || strings.TrimSpace(out) != "beta [news]: hi" {
		t.Errorf("%v %q", err, out)
	}
}
