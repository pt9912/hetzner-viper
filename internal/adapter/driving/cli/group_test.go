package cli_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

func TestGroupOptionsAreParsedAndExcludeAMachine(t *testing.T) {
	name, o, err := cli.Parse([]string{"check", "--group", "dev", "--parallel", "3"})
	if err != nil || name != "check" || o.Group != "dev" || o.Parallel != 3 || o.Machine != "" {
		t.Fatalf("%v %+v", err, o)
	}
	if _, _, err := cli.Parse([]string{"ssh", "--group=dev", "--cmd", "uptime"}); err != nil {
		t.Error(err)
	}
	for _, args := range [][]string{
		{"check", "web", "--group", "dev"},
		{"check", "--parallel", "0", "--group", "dev"},
		{"check", "--parallel", "x", "--group", "dev"},
		{"check"},
		{"find", "--group", "dev"},
		{"down", "--group", "dev"}, // not yet
	} {
		if _, _, err := cli.Parse(args); err == nil {
			t.Errorf("%v must be refused", args)
		}
	}
}

func TestThePrefixWriterWritesWholeLinesWithTheNameOfTheMachine(t *testing.T) {
	var out bytes.Buffer
	ws, flush := cli.NewPrefixWriters(&out, 4, "a", "beta")
	// the machines say their lines in pieces, at the same time
	fmt.Fprint(ws[0], "hel")
	fmt.Fprint(ws[1], "one\ntw")
	fmt.Fprint(ws[0], "lo\n\n")
	fmt.Fprint(ws[1], "o\r50%\r100%\r\nrest")
	flush()
	want := []string{"[beta] one", "[a]    hello", "[beta] two", "[beta] 50%", "[beta] 100%", "[beta] rest"}
	for _, w := range want {
		if !strings.Contains(out.String(), w+"\n") {
			t.Errorf("missing %q in\n%s", w, out.String())
		}
	}
	if strings.Contains(out.String(), "\n\n") || strings.Contains(out.String(), "\r") {
		t.Errorf("empty lines and returns are dropped:\n%q", out.String())
	}
}

func groupDir(t *testing.T, machines string) (string, settings.Env) {
	t.Helper()
	env, _ := nativeEnv(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "groups"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "groups", "dev.env"), []byte("GROUP_MACHINES="+machines+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, env
}

func TestAGroupCommandRunsForEveryMachineWithPrefixesAndPrintsTheTable(t *testing.T) {
	dir, env := groupDir(t, "alpha,beta,gamma")
	_, h := fakeTools(t)
	var mu sync.Mutex
	var ran []string
	var out, errb bytes.Buffer
	err := cli.RunGroup(context.Background(), &cli.Opts{Host: h, Dir: dir, Group: "dev"}, env, &out, &errb,
		func(_ context.Context, p *cli.Opts, _ settings.Env, o, e io.Writer) error {
			mu.Lock()
			ran = append(ran, p.Machine)
			mu.Unlock()
			fmt.Fprintf(o, "hello from %s\n", p.Machine)
			switch p.Machine {
			case "beta":
				fmt.Fprintln(e, "beta is unhappy")
				return cli.ExitStatus(1)
			case "gamma":
				return errors.New("no SSH config")
			}
			return nil
		})
	if cli.ExitCode(err) != 1 {
		t.Fatalf("a failed machine: exit status 1, got %v", err)
	}
	if len(ran) != 3 {
		t.Errorf("ran for %v", ran)
	}
	for _, want := range []string{"[alpha] hello from alpha\n", "[beta]  hello from beta\n", "[gamma] hello from gamma\n", "== Result", "alpha  ok", "beta   failed (exit status 1", "gamma  failed: no SSH config"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in\n%s", want, out.String())
		}
	}
	if !strings.Contains(errb.String(), "[beta]  beta is unhappy\n") || !strings.Contains(errb.String(), "[gamma] no SSH config\n") {
		t.Errorf("stderr:\n%s", errb.String())
	}
}

func TestAGroupCommandForAGroupThatDoesNotExistSaysSo(t *testing.T) {
	dir, env := groupDir(t, "alpha")
	_, h := fakeTools(t)
	err := cli.RunGroup(context.Background(), &cli.Opts{Host: h, Dir: dir, Group: "nope"}, env, io.Discard, io.Discard,
		func(context.Context, *cli.Opts, settings.Env, io.Writer, io.Writer) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "No group nope") {
		t.Errorf("%v", err)
	}
}

func TestSSHForAGroupNeedsACommand(t *testing.T) {
	dir, env := groupDir(t, "alpha")
	_ = env
	_, h := fakeTools(t)
	for _, p := range []*cli.Opts{{Host: h, Dir: dir, Group: "dev"}, {Host: h, Dir: dir, Group: "dev", Cmd: "x", Tmux: true}} {
		if err := cli.RunCommand("ssh", p); err == nil || !strings.Contains(err.Error(), "needs --cmd") {
			t.Errorf("%v", err)
		}
	}
}
