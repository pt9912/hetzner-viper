package main

import (
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

func TestNativeCommandsRunHereAndContainerFlagRunsThemInTheContainer(t *testing.T) {
	var ran []string
	oldNative, oldIn := commands["setup"], inContainer
	t.Cleanup(func() { commands["setup"], inContainer = oldNative, oldIn })
	c := oldNative
	c.native = func(*options) error { ran = append(ran, "native"); return nil }
	commands["setup"] = c
	inContainer = func(command, options) error { ran = append(ran, "container"); return nil }

	_, o := mustParse(t, "setup")
	dispatch(commands["setup"], &o)
	_, o = mustParse(t, "setup", "--container")
	dispatch(commands["setup"], &o)
	_, o = mustParse(t, "up", "web") // has no native version yet
	dispatch(commands["up"], &o)
	if !slices.Equal(ran, []string{"native", "container", "container"}) {
		t.Errorf("ran %q", ran)
	}
	if _, _, err := parse([]string{"up", "web", "--container"}); err != nil {
		t.Errorf("--container is global: %v", err)
	}
}

func TestSetupResolvesARelativeKeyInTheSettingsDirectory(t *testing.T) {
	env, _ := nativeEnv(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\nSSH_KEY=keys/rel\n"), 0o600)
	calls := fakeTools(t)
	if err := setup(&options{dir: dir}, env, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[0][4]; got != filepath.Join(dir, "keys", "rel") {
		t.Errorf("key at %q", got)
	}
}

func TestSetupRefusesADomainThatIsNotOne(t *testing.T) {
	env, home := nativeEnv(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=\"a.com\nb\"\n"), 0o600)
	calls := fakeTools(t)
	if err := setup(&options{dir: dir}, env, io.Discard); err == nil || !strings.Contains(err.Error(), "DNS_DOMAIN must be a domain") {
		t.Errorf("got %v", err)
	}
	if len(*calls) != 0 || exists(filepath.Join(home, ".ssh")) {
		t.Errorf("nothing created: %q", *calls)
	}
}

func TestMissingOpenSSHIsReportedBeforeAnythingIsCreated(t *testing.T) {
	env, home := nativeEnv(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\n"), 0o600)
	fakeTools(t)
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	err := setup(&options{dir: dir}, env, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "--container") || !strings.Contains(err.Error(), "ssh-keygen") {
		t.Fatalf("got %v", err)
	}
	if exists(filepath.Join(home, ".ssh")) {
		t.Error("~/.ssh was created before the check")
	}
	// ssh: the same
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	os.WriteFile(filepath.Join(home, ".ssh", "config_hetzner_claude-web"), nil, 0o600)
	if err := connect(&options{dir: dir, machine: "web"}, env, false); err == nil || !strings.Contains(err.Error(), "--container") {
		t.Errorf("ssh: got %v", err)
	}
	// keys that exist need no ssh-keygen
	os.WriteFile(filepath.Join(home, ".ssh", "hetzner-build"), nil, 0o600)
	os.WriteFile(filepath.Join(home, ".ssh", "hetzner_claude_host_ca"), nil, 0o600)
	os.WriteFile(filepath.Join(home, ".ssh", "hetzner_claude_host_ca.pub"), []byte("ssh-ed25519 AAAA c\n"), 0o644)
	if err := setup(&options{dir: dir}, env, io.Discard); err != nil {
		t.Errorf("existing keys: %v", err)
	}
}

func TestSetupCreatesTheTemplateOnlyOnce(t *testing.T) {
	env, _ := nativeEnv(t)
	dir := t.TempDir()
	fakeTools(t)
	var out strings.Builder
	setup(&options{dir: dir}, env, &out)
	if !strings.Contains(out.String(), "Created ") {
		t.Errorf("says it created the file: %q", out.String())
	}
	os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=mine.example.com\n"), 0o600)
	out.Reset()
	if err := setup(&options{dir: dir}, env, &out); err != nil || strings.Contains(out.String(), "Created ") {
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
	os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\n"), 0o600)
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
		syscall.Kill(os.Getpid(), syscall.SIGTERM)
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
		syscall.Kill(os.Getpid(), syscall.SIGINT)
	}()
	if err := runChild("sleep", "1"); err != nil {
		t.Errorf("viper survives SIGINT and waits: %v", err)
	}
}

func TestLogReadsThePrefixAndShowsTheMachine(t *testing.T) {
	env, home := nativeEnv(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\nSERVER_PREFIX=cs # c\n"), 0o600)
	state := filepath.Join(home, ".local", "state", "hetzner-claude")
	os.MkdirAll(filepath.Join(state, "logs"), 0o700)
	os.WriteFile(filepath.Join(state, "events.log"), []byte("t cs-web up ready\nt claude-api up ready\n"), 0o600)
	os.WriteFile(filepath.Join(state, "logs", "cs-web.log"), []byte("full output\n"), 0o600)
	var out strings.Builder
	if err := showLog(&options{dir: dir, machine: "web"}, env, &out); err != nil || out.String() != "t cs-web up ready\n" {
		t.Errorf("%q %v", out.String(), err)
	}
	out.Reset()
	showLog(&options{dir: dir, machine: "web", full: true}, env, &out)
	if out.String() != "full output\n" {
		t.Errorf("%q", out.String())
	}
	out.Reset()
	showLog(&options{dir: dir}, env, &out)
	if !strings.Contains(out.String(), "claude-api") || !strings.Contains(out.String(), "cs-web") {
		t.Errorf("all: %q", out.String())
	}
	if commands["log"].native == nil {
		t.Error("log is native")
	}
}
