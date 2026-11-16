package terminal_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/terminal"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func have(names ...string) func(string) (string, error) {
	return func(n string) (string, error) {
		for _, x := range names {
			if x == n {
				return "/bin/" + n, nil
			}
		}
		return "", errors.New("not found")
	}
}

func wordsOf() []string {
	return []string{"/opt/my viper/viper", "terminal", "web", "--here", "--dir", "/home/a b/it's"}
}

func TestLinuxTakesTheFirstProgramOfTheListAndGivesTheWordsOneByOne(t *testing.T) {
	cases := []struct {
		have   []string
		want   string
		before []string
	}{
		{[]string{"xterm", "konsole"}, "/bin/konsole", []string{"-e"}},
		{[]string{"gnome-terminal", "xterm"}, "/bin/gnome-terminal", []string{"--"}},
		{[]string{"xfce4-terminal"}, "/bin/xfce4-terminal", []string{"-x"}},
		{[]string{"kitty"}, "/bin/kitty", nil},
		{[]string{"alacritty"}, "/bin/alacritty", []string{"-e"}},
		{[]string{"wezterm"}, "/bin/wezterm", []string{"start", "--"}},
		{[]string{"xterm"}, "/bin/xterm", []string{"-e"}},
	}
	for _, c := range cases {
		name, args, err := terminal.Plan("linux", env(map[string]string{"DISPLAY": ":0"}), have(c.have...), wordsOf())
		if err != nil || name != c.want || !reflect.DeepEqual(args, append(append([]string(nil), c.before...), wordsOf()...)) {
			t.Errorf("%v: %s %q %v", c.have, name, args, err)
		}
	}
	// x-terminal-emulator is not used: its -e may take one line of a shell
	if _, _, err := terminal.Plan("linux", env(map[string]string{"WAYLAND_DISPLAY": "wayland-0"}), have("x-terminal-emulator"), wordsOf()); err == nil || !strings.Contains(err.Error(), "no terminal program found") {
		t.Errorf("x-terminal-emulator: %v", err)
	}
}

func TestVIPERTERMINALNamesOneOfTheListAndNothingElse(t *testing.T) {
	e := env(map[string]string{"DISPLAY": ":0", "VIPER_TERMINAL": "xterm"})
	name, _, err := terminal.Plan("linux", e, have("gnome-terminal", "xterm"), wordsOf())
	if err != nil || name != "/bin/xterm" {
		t.Errorf("%s %v", name, err)
	}
	for _, bad := range []string{"/bin/sh", "sh -c id", "rm", "x-terminal-emulator"} {
		e := env(map[string]string{"DISPLAY": ":0", "VIPER_TERMINAL": bad})
		if _, _, err := terminal.Plan("linux", e, have("gnome-terminal", "xterm", "rm", "sh", "x-terminal-emulator"), wordsOf()); err == nil || !strings.Contains(err.Error(), "VIPER_TERMINAL") {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, _, err := terminal.Plan("linux", env(map[string]string{"DISPLAY": ":0", "VIPER_TERMINAL": "kitty"}), have("xterm"), wordsOf()); err == nil || !strings.Contains(err.Error(), "no terminal program found") {
		t.Errorf("a named program that is missing: %v", err)
	}
}

func TestWithoutADesktopThereIsNoWindow(t *testing.T) {
	if _, _, err := terminal.Plan("linux", env(nil), have("xterm"), wordsOf()); err == nil || !strings.Contains(err.Error(), "no desktop") {
		t.Errorf("%v", err)
	}
}

func TestWindowsUsesWindowsTerminalAndRefusesASemicolon(t *testing.T) {
	name, args, err := terminal.Plan("windows", env(nil), have("wt.exe"), wordsOf())
	if err != nil || name != "/bin/wt.exe" || !reflect.DeepEqual(args, append([]string{"new-tab"}, wordsOf()...)) {
		t.Errorf("%s %q %v", name, args, err)
	}
	if _, _, err := terminal.Plan("windows", env(nil), have(), wordsOf()); err == nil || !strings.Contains(err.Error(), "Windows Terminal not found") {
		t.Errorf("%v", err)
	}
	if _, _, err := terminal.Plan("windows", env(nil), have("wt.exe"), []string{"C:\\a;b\\viper.exe", "terminal", "web"}); err == nil || !strings.Contains(err.Error(), ";") {
		t.Errorf("a semicolon: %v", err)
	}
	if _, _, err := terminal.Plan("plan9", env(nil), have(), wordsOf()); err == nil {
		t.Errorf("an unknown system")
	}
}

func TestTheMacScriptQuotesEveryWordForTheShell(t *testing.T) {
	got := terminal.MacScript(wordsOf())
	want := "#!/bin/sh\nexec '/opt/my viper/viper' 'terminal' 'web' '--here' '--dir' '/home/a b/it'\\''s'\n"
	if got != want {
		t.Errorf("%q", got)
	}
	// run by a shell, the wordsOf() come back as they were
	if os.PathSeparator != '/' {
		return
	}
	script := filepath.Join(t.TempDir(), "s.sh")
	if err := os.WriteFile(script, []byte(terminal.MacScript([]string{"printf", "%s|", "a b", "it's", "$HOME", "`id`", ";x", "\"q\""})), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", script).Output()
	if err != nil || string(out) != "a b|it's|$HOME|`id`|;x|\"q\"|" {
		t.Errorf("%q %v", out, err)
	}
}

// a real start: a fake terminal program that runs in the foreground does not hold viper; one that fails at once is reported; one that ends at once with 0 is fine
func TestLaunchDoesNotWaitForTheWindowAndReportsAProgramThatFailsAtOnce(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("shell scripts")
	}
	dir := t.TempDir()
	fake := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(dir, "log")
	fake("xterm", "printf '%s\\n' \"$@\" > "+log+"\nexec sleep 5\n")
	l := terminal.Launcher{GOOS: "linux", Env: os.Environ(), Getenv: env(map[string]string{"DISPLAY": ":0"}), LookPath: func(n string) (string, error) {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			return "", err
		}
		return filepath.Join(dir, n), nil
	}}
	start := nowMs()
	if err := l.Launch(wordsOf()); err != nil {
		t.Fatal(err)
	}
	if took := nowMs() - start; took > 3000 {
		t.Errorf("Launch waited %d ms for a window that stays", took)
	}
	b, _ := os.ReadFile(log)
	if got := strings.Split(strings.TrimSpace(string(b)), "\n"); !reflect.DeepEqual(got, append([]string{"-e"}, wordsOf()...)) {
		t.Errorf("arguments %q", got)
	}
	fake("xterm", "echo 'cannot open display' >&2\nexit 1\n")
	err := l.Launch(wordsOf())
	var exit *exec.ExitError
	if err == nil || !strings.Contains(err.Error(), "cannot open display") || errors.As(err, &exit) {
		t.Errorf("a failure at once is said, and is no *exec.ExitError (viper would take it for the status of a program): %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(os.TempDir(), "viper-terminal-*")); len(left) != 0 && os.Getenv("VIPER_TEST_KEEP") == "" {
		for _, f := range left {
			if st, e := os.Stat(f); e == nil && time.Since(st.ModTime()) < time.Minute {
				t.Errorf("a file of the error output is left: %s", f)
			}
		}
	}
	fake("xterm", "exit 0\n")
	if err := l.Launch(wordsOf()); err != nil {
		t.Errorf("a program that hands the window over: %v", err)
	}
}

func nowMs() int64 { return time.Now().UnixMilli() }
