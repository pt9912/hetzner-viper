package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSetupResolvesARelativeKeyInTheSettingsDirectory(t *testing.T) {
	env, _ := nativeEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\nSSH_KEY=keys/rel\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls, h := fakeTools(t)
	if err := setup(&options{h: h, dir: dir}, env, io.Discard); err != nil {
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
	if err := setup(&options{h: h, dir: dir}, env, io.Discard); err == nil || !strings.Contains(err.Error(), "DNS_DOMAIN must be a domain") {
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
	h.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	err := setup(&options{h: h, dir: dir}, env, io.Discard)
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
	if err := connect(&options{h: h, dir: dir, machine: "web"}, env, false); err == nil || !strings.Contains(err.Error(), "OpenSSH") {
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
	if err := setup(&options{h: h, dir: dir}, env, io.Discard); err != nil {
		t.Errorf("existing keys: %v", err)
	}
}

func TestSetupCreatesTheTemplateOnlyOnce(t *testing.T) {
	env, _ := nativeEnv(t)
	dir := t.TempDir()
	_, h := fakeTools(t)
	var out strings.Builder
	_ = setup(&options{h: h, dir: dir}, env, &out)
	if !strings.Contains(out.String(), "Created ") {
		t.Errorf("says it created the file: %q", out.String())
	}
	if st, err := os.Stat(filepath.Join(dir, ".env.local")); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("a new .env.local holds tokens and must be private: %v %v", st, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=mine.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := setup(&options{h: h, dir: dir}, env, &out); err != nil || strings.Contains(out.String(), "Created ") {
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
	if err := setup(&options{dir: dir}, env, io.Discard); err != nil {
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

func TestChildExitCodesAndSignals(t *testing.T) {
	err := runChild("sh", "-c", "exit 3")
	if exitCode(err) != 3 {
		t.Errorf("exit code %d (%v)", exitCode(err), err)
	}
	if exitCode(errors.New("x")) != 1 {
		t.Error("other errors are 1")
	}
	// SIGTERM sent to viper is passed on to the child
	go func() {
		time.Sleep(300 * time.Millisecond)
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			t.Error(err)
		}
	}()
	start := time.Now()
	err = runChild("sleep", "30")
	if err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("the child was not ended: %v after %v", err, time.Since(start))
	}
	if exitCode(err) != 130 {
		t.Errorf("a child ended by a signal gives %d", exitCode(err))
	}
	// SIGINT (Ctrl-C) reaches the child with the terminal, not through us; viper only survives it
	go func() {
		time.Sleep(300 * time.Millisecond)
		if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
			t.Error(err)
		}
	}()
	if err := runChild("sleep", "1"); err != nil {
		t.Errorf("viper survives SIGINT and waits: %v", err)
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
	if err := showLog(&options{dir: dir, machine: "web"}, env, &out); err != nil || out.String() != "t cs-web up ready\n" {
		t.Errorf("%q %v", out.String(), err)
	}
	out.Reset()
	_ = showLog(&options{dir: dir, machine: "web", full: true}, env, &out)
	if out.String() != "full output\n" {
		t.Errorf("%q", out.String())
	}
	out.Reset()
	_ = showLog(&options{dir: dir}, env, &out)
	if !strings.Contains(out.String(), "claude-api") || !strings.Contains(out.String(), "cs-web") {
		t.Errorf("all: %q", out.String())
	}
	if commands()["log"].run == nil {
		t.Error("log is native")
	}
}

func TestAskerAnswersLikeReadDashR(t *testing.T) {
	ask := func(input string, prompts int) []string {
		a := newAsker(context.Background(), strings.NewReader(input), io.Discard)
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
	a := newAsker(ctx, strings.NewReader("DELETE\nDELETE\n"), &shown)
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
	b := newAsker(ctx2, pr, io.Discard)
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
	h.lookPath = func(p string) (string, error) {
		if missing[p] {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + p, nil
	}
	for _, c := range []struct {
		command string
		program string
		run     func(*options) error
	}{
		{"up", "ssh", nativeUp}, {"up", "ssh-keygen", nativeUp}, {"up", "scp", nativeUp}, {"down", "ssh", nativeDown}, {"check", "ssh", nativeCheck},
	} {
		missing = map[string]bool{c.program: true}
		err := c.run(&options{h: h, machine: "web", dir: t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), c.program+" not found") || !strings.Contains(err.Error(), "OpenSSH") {
			t.Errorf("%s without %s: %v", c.command, c.program, err)
		}
	}
}
