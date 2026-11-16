package cli_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

// the window runs viper terminal NAME --here with the settings directory as an absolute path; every word is one word
func TestTheWindowRunsViperTerminalHereWithEveryWordAsItIs(t *testing.T) {
	got := cli.TerminalWords("/opt/my viper/viper", "web", "dev", "/home/a b/it's/dir")
	want := []string{"/opt/my viper/viper", "terminal", "web", "--in-group", "dev", "--here", "--dir", "/home/a b/it's/dir"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%q", got)
	}
	if got := cli.TerminalWords("viper", "web", "", "/d"); !reflect.DeepEqual(got, []string{"viper", "terminal", "web", "--here", "--dir", "/d"}) {
		t.Errorf("%q", got)
	}
}

func terminalSetup(t *testing.T, ssh bool) (*cli.Opts, *[][]string, *cli.Host, settings.Env) {
	t.Helper()
	env, home := nativeEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ssh {
		cfg := filepath.Join(home, ".ssh", "config_hetzner_claude-web")
		if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfg, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	calls, h := fakeTools(t)
	return &cli.Opts{Host: h, Dir: dir, Machine: "web"}, calls, h, env
}

func TestViperTerminalOpensTheWindowWithTheWordsAndNeedsTheSshConfig(t *testing.T) {
	o, _, h, env := terminalSetup(t, false)
	var opened [][]string
	h.Services.LaunchTerminal = func(title string, words []string) error { opened = append(opened, words); return nil }
	if err := cli.Terminal(o, env); err == nil || !strings.Contains(err.Error(), "run viper up web first") {
		t.Fatalf("without the SSH config: %v", err)
	}
	if len(opened) != 0 {
		t.Errorf("a window was opened: %q", opened)
	}
}

// --here: ssh is a child (RunTool), not a replacement (ExecTool); on an error the message stays and Enter is waited for
func TestHereRunsSshAsAChildAndWaitsForEnterOnlyOnAnError(t *testing.T) {
	o, calls, h, env := terminalSetup(t, true)
	var execs int
	h.ExecTool = func(string, ...string) error { execs++; return nil }
	var errOut bytes.Buffer
	h.Stderr = &errOut
	h.IsTerminal = func() bool { return true }
	h.Stdin = strings.NewReader("\n")
	var screen bytes.Buffer
	h.Stdout = &screen
	if err := cli.TerminalHere(o, env); err != nil {
		t.Fatal(err)
	}
	if screen.String() != "\x1b]0;web\x07" {
		t.Errorf("the title of the window: %q", screen.String())
	}
	if !strings.Contains(strings.Join((*calls)[0], " "), "set-option status-left '[web] '") {
		t.Errorf("the status line names the machine: %q", (*calls)[0])
	}
	if execs != 0 || len(*calls) != 1 || (*calls)[0][0] != "ssh" || !strings.Contains(strings.Join((*calls)[0], " "), "exec tmux new-session -A -s claude") || (*calls)[0][1] != "-t" {
		t.Errorf("exec %d, calls %q", execs, *calls)
	}
	if strings.Contains(errOut.String(), "Press Enter") {
		t.Errorf("waited after a good end: %q", errOut.String())
	}
	// ssh fails: the message and the wait for a line
	in := &countingReader{data: "go on\n"}
	h.Stdin = in
	h.RunTool = func(string, ...string) error { return errors.New("exit status 127") }
	err := cli.TerminalHere(o, env)
	if err == nil || !strings.Contains(errOut.String(), "exit status 127") || !strings.Contains(errOut.String(), "Press Enter") || !in.read {
		t.Errorf("err %v, out %q, read %v", err, errOut.String(), in.read)
	}
	// no terminal (a run of the pages): ssh refuses, and nobody waits
	in2 := &countingReader{data: "x\n"}
	h.Stdin, h.IsTerminal = in2, func() bool { return false }
	errOut.Reset()
	if err := cli.TerminalHere(o, env); err == nil || !strings.Contains(err.Error(), "needs a terminal") || in2.read || strings.Contains(errOut.String(), "Press Enter") {
		t.Errorf("err %v, read %v, out %q", err, in2.read, errOut.String())
	}
}

type countingReader struct {
	data string
	read bool
}

func (r *countingReader) Read(p []byte) (int, error) {
	r.read = true
	n := copy(p, r.data)
	r.data = r.data[n:]
	if n == 0 {
		return 0, errors.New("eof")
	}
	return n, nil
}

// a machine that a group names: the title of the window and the status line say the same, with the group
func TestTheTitleAndTheStatusLineNameTheGroupOfTheMachine(t *testing.T) {
	o, calls, h, env := terminalSetup(t, true)
	if err := os.MkdirAll(filepath.Join(o.Dir, "groups"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.Dir, "groups", "dev.env"), []byte("GROUP_MACHINES=web\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var screen bytes.Buffer
	h.Stdout, h.IsTerminal = &screen, func() bool { return true }
	if err := cli.TerminalHere(o, env); err != nil {
		t.Fatal(err)
	}
	if screen.String() != "\x1b]0;web (dev)\x07" || !strings.Contains(strings.Join((*calls)[0], " "), "set-option status-left '[web (dev)] '") {
		t.Errorf("title %q, call %q", screen.String(), (*calls)[0])
	}
}
