package cli_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
)

// vscodeFixture is a settings directory and a machine "web" with its ssh config.
func vscodeFixture(t *testing.T) (*cli.Opts, string) {
	t.Helper()
	env, home := nativeEnv(t)
	t.Setenv("HOME", env.Home)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(home, ".ssh", "config_hetzner_claude-web")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return &cli.Opts{Host: testHost(), Dir: dir, Machine: "web"}, cfg
}

func TestVSCodeOpensTheWorkDirectoryOfTheMachine(t *testing.T) {
	env, _ := nativeEnv(t)
	o, cfg := vscodeFixture(t)
	env.Home = filepath.Dir(filepath.Dir(cfg))
	_, h := fakeTools(t)
	var asked []string
	h.OutputTool = func(name string, args ...string) (string, error) {
		asked = append([]string{name}, args...)
		return "/home/builder/work/web\n", nil
	}
	var alias, workdir string
	h.Services.LaunchVSCode = func(a, w string) error { alias, workdir = a, w; return nil }
	o.Host = h
	if err := cli.OpenVSCode(o, env); err != nil {
		t.Fatal(err)
	}
	if alias != "hetzner-claude-web" || workdir != "/home/builder/work/web" {
		t.Errorf("opened %q %q", alias, workdir)
	}
	want := []string{"ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-F", cfg, "hetzner-claude-web", "cat ~/.config/viper/workdir"}
	if !reflect.DeepEqual(asked, want) {
		t.Errorf("asked %q", asked)
	}
}

func TestVSCodeOpensWithoutAFolderIfTheWorkDirectoryIsUnknown(t *testing.T) {
	for name, answer := range map[string]struct {
		out string
		err error
	}{"ssh fails": {"", errors.New("exit 255")}, "relative": {"work\n", nil}, "empty": {"", nil}} {
		env, _ := nativeEnv(t)
		o, cfg := vscodeFixture(t)
		env.Home = filepath.Dir(filepath.Dir(cfg))
		_, h := fakeTools(t)
		h.OutputTool = func(string, ...string) (string, error) { return answer.out, answer.err }
		got := "unset"
		h.Services.LaunchVSCode = func(_, w string) error { got = w; return nil }
		o.Host = h
		if err := cli.OpenVSCode(o, env); err != nil || got != "" {
			t.Errorf("%s: %q %v", name, got, err)
		}
	}
}

func TestVSCodeNeedsTheMachineToHaveBeenSetUp(t *testing.T) {
	env, _ := nativeEnv(t)
	o, _ := vscodeFixture(t) // its ssh config is under another home than env's
	_, h := fakeTools(t)
	h.Services.LaunchVSCode = func(string, string) error { t.Error("VS Code was started"); return nil }
	o.Host = h
	if err := cli.OpenVSCode(o, env); err == nil || !strings.Contains(err.Error(), "run viper up web first") {
		t.Errorf("got %v", err)
	}
}

func TestVSCodeReportsAMissingCodeCommand(t *testing.T) {
	env, _ := nativeEnv(t)
	o, cfg := vscodeFixture(t)
	env.Home = filepath.Dir(filepath.Dir(cfg))
	_, h := fakeTools(t)
	h.OutputTool = func(string, ...string) (string, error) { return "/w\n", nil }
	h.Services.LaunchVSCode = func(string, string) error { return errors.New("code not found") }
	o.Host = h
	if err := cli.OpenVSCode(o, env); err == nil || !strings.Contains(err.Error(), "code not found") {
		t.Errorf("got %v", err)
	}
}
