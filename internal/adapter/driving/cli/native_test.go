package cli_test

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
)

func TestSetupResolvesARelativeKeyInTheSettingsDirectory(t *testing.T) {
	env, _ := nativeEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\nSSH_KEY=keys/rel\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls, h := fakeTools(t)
	if err := cli.Setup(&cli.Opts{Host: h, Dir: dir}, env, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[0][4]; got != filepath.Join(dir, "keys", "rel") {
		t.Errorf("key at %q", got)
	}
}

func TestSetupRefusesADomainThatIsNotOne(t *testing.T) {
	env, home := nativeEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=\"a.com\nb\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls, h := fakeTools(t)
	if err := cli.Setup(&cli.Opts{Host: h, Dir: dir}, env, io.Discard); err == nil || !strings.Contains(err.Error(), "DNS_DOMAIN must be a domain") {
		t.Errorf("got %v", err)
	}
	if len(*calls) != 0 || exists(filepath.Join(home, ".ssh")) {
		t.Errorf("nothing created: %q", *calls)
	}
}

func TestMissingOpenSSHIsReportedBeforeAnythingIsCreated(t *testing.T) {
	env, home := nativeEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, h := fakeTools(t)
	h.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	err := cli.Setup(&cli.Opts{Host: h, Dir: dir}, env, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "OpenSSH") || !strings.Contains(err.Error(), "ssh-keygen") {
		t.Fatalf("got %v", err)
	}
	if exists(filepath.Join(home, ".ssh")) {
		t.Error("~/.ssh was created before the check")
	}
	// ssh: the same
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config_hetzner_claude-web"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cli.Connect(&cli.Opts{Host: h, Dir: dir, Machine: "web"}, env, false); err == nil || !strings.Contains(err.Error(), "OpenSSH") {
		t.Errorf("ssh: got %v", err)
	}
	// keys that exist need no ssh-keygen
	if err := os.WriteFile(filepath.Join(home, ".ssh", "hetzner-build"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "hetzner_claude_host_ca"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "hetzner_claude_host_ca.pub"), []byte("ssh-ed25519 AAAA c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cli.Setup(&cli.Opts{Host: h, Dir: dir}, env, io.Discard); err != nil {
		t.Errorf("existing keys: %v", err)
	}
}

func TestSetupCreatesTheTemplateOnlyOnce(t *testing.T) {
	env, _ := nativeEnv(t)
	dir := t.TempDir()
	_, h := fakeTools(t)
	var out strings.Builder
	_ = cli.Setup(&cli.Opts{Host: h, Dir: dir}, env, &out)
	if !strings.Contains(out.String(), "Created ") {
		t.Errorf("says it created the file: %q", out.String())
	}
	if b, err := os.ReadFile(filepath.Join(dir, "machines", "example.env.local.example")); err != nil || !strings.Contains(string(b), "REPO_URL=") {
		t.Errorf("the template for one machine: %v %q", err, b)
	}
	if st, err := os.Stat(filepath.Join(dir, ".env.local")); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("a new .env.local holds tokens and must be private: %v %v", st, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=mine.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := cli.Setup(&cli.Opts{Host: h, Dir: dir}, env, &out); err != nil || strings.Contains(out.String(), "Created ") {
		t.Errorf("an existing file is kept: %v %q", err, out.String())
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".env.local")); string(b) != "DNS_DOMAIN=mine.example.com\n" {
		t.Errorf("file changed: %q", b)
	}
}

func TestRealSshKeygenAcceptsWhatSetupAsks(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("no ssh-keygen")
	}
	// it needs a passwd entry for our uid (missing in some containers)
	if err := exec.Command("ssh-keygen", "-t", "ed25519", "-f", filepath.Join(t.TempDir(), "probe"), "-N", "").Run(); err != nil {
		t.Skip("ssh-keygen does not run here: ", err)
	}
	env, home := nativeEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cli.Setup(&cli.Opts{Host: testHost(), Dir: dir}, env, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"hetzner-build", "hetzner-build.pub", "hetzner_claude_host_ca", "hetzner_claude_host_ca.pub", "known_hosts_hetzner_claude_ca"} {
		if !exists(filepath.Join(home, ".ssh", f)) {
			t.Errorf("%s missing", f)
		}
	}
	if st, _ := os.Stat(filepath.Join(home, ".ssh", "hetzner-build")); st.Mode().Perm() != 0o600 {
		t.Errorf("private key mode %v", st.Mode())
	}
}

func TestExitCodeOfAChildAndOfOtherErrors(t *testing.T) {
	err := exec.CommandContext(context.Background(), "sh", "-c", "exit 3").Run()
	if cli.ExitCode(err) != 3 {
		t.Errorf("exit code %d (%v)", cli.ExitCode(err), err)
	}
	if cli.ExitCode(errors.New("x")) != 1 {
		t.Error("other errors are 1")
	}
	err = exec.CommandContext(context.Background(), "sh", "-c", "kill -TERM $$").Run()
	if cli.ExitCode(err) != 130 {
		t.Errorf("a child ended by a signal gives %d (%v)", cli.ExitCode(err), err)
	}
}

func TestLogReadsThePrefixAndShowsTheMachine(t *testing.T) {
	env, home := nativeEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\nSERVER_PREFIX=cs # c\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(home, ".local", "state", "hetzner-claude")
	if err := os.MkdirAll(filepath.Join(state, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "events.log"), []byte("t cs-web up ready\nt claude-api up ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "logs", "cs-web.log"), []byte("full output\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := cli.ShowLog(&cli.Opts{Host: testHost(), Dir: dir, Machine: "web"}, env, &out); err != nil || out.String() != "t cs-web up ready\n" {
		t.Errorf("%q %v", out.String(), err)
	}
	out.Reset()
	_ = cli.ShowLog(&cli.Opts{Host: testHost(), Dir: dir, Machine: "web", Full: true}, env, &out)
	if out.String() != "full output\n" {
		t.Errorf("%q", out.String())
	}
	out.Reset()
	_ = cli.ShowLog(&cli.Opts{Host: testHost(), Dir: dir}, env, &out)
	if !strings.Contains(out.String(), "claude-api") || !strings.Contains(out.String(), "cs-web") {
		t.Errorf("all: %q", out.String())
	}
	if !cli.IsNative("log") {
		t.Error("log is native")
	}
}

func TestAskerAnswersLikeReadDashR(t *testing.T) {
	ask := func(input string, prompts int) []string {
		a := cli.NewAsker(context.Background(), strings.NewReader(input), io.Discard)
		var out []string
		for i := 0; i < prompts; i++ {
			out = append(out, a("? "))
		}
		return out
	}
	for _, c := range []struct {
		input string
		want  []string
	}{
		{"DELETE\n", []string{"DELETE", ""}},
		{"DISCARD\nDELETE\n", []string{"DISCARD", "DELETE"}},
		{"  DELETE \t\n", []string{"DELETE", ""}},
		{"DELETE\r\n", []string{"DELETE\r", ""}},
		{"DELETE", []string{"", ""}},
		{"DISCARD\nDELETE", []string{"DISCARD", ""}},
		{"", []string{"", ""}},
		{"\n\nDELETE\n", []string{"", ""}},
		{"delete\n", []string{"delete", ""}},
	} {
		if got := ask(c.input, 2); !slices.Equal(got, c.want) {
			t.Errorf("%q: got %q, want %q", c.input, got, c.want)
		}
	}
}

func TestAskerNeverConfirmsAfterAnInterruption(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var shown strings.Builder
	a := cli.NewAsker(ctx, strings.NewReader("DELETE\nDELETE\n"), &shown)
	cancel()
	for i := 0; i < 50; i++ {
		if got := a("Type DELETE: "); got != "" {
			t.Fatalf("answered %q after the interruption", got)
		}
	}
	if !strings.Contains(shown.String(), "Type DELETE: ") {
		t.Error("prompt not shown")
	}
	// a blocked read ends with the interruption
	pr, _ := io.Pipe()
	ctx2, cancel2 := context.WithCancel(context.Background())
	b := cli.NewAsker(ctx2, pr, io.Discard)
	done := make(chan string)
	go func() { done <- b("? ") }()
	time.Sleep(50 * time.Millisecond)
	cancel2()
	select {
	case got := <-done:
		if got != "" {
			t.Errorf("got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the prompt did not end with the interruption")
	}
}

func TestUpDownAndCheckNeedOpenSSHBeforeAnythingHappens(t *testing.T) {
	_, h := fakeTools(t)
	missing := map[string]bool{}
	h.LookPath = func(p string) (string, error) {
		if missing[p] {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + p, nil
	}
	for _, c := range []struct {
		command string
		program string
	}{
		{"up", "ssh"}, {"up", "ssh-keygen"}, {"up", "scp"}, {"down", "ssh"}, {"check", "ssh"},
	} {
		missing = map[string]bool{c.program: true}
		err := cli.RunCommand(c.command, &cli.Opts{Host: h, Machine: "web", Dir: t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), c.program+" not found") || !strings.Contains(err.Error(), "OpenSSH") {
			t.Errorf("%s without %s: %v", c.command, c.program, err)
		}
	}
}
