package start

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The golden files were made by the Python code of start.sh from the same inputs.
func TestUserDataIsWhatTheScriptMade(t *testing.T) {
	base := []byte(read(t, "cloud-init.yaml"))
	for name, keys := range map[string][]string{
		"one": {read(t, "login.pub")},
		"two": {read(t, "login.pub"), read(t, "ipad.pub")},
	} {
		got, err := UserData(base, keys, read(t, "host_key"), read(t, "host_key.pub"), read(t, "host_key-cert.pub"))
		if err != nil {
			t.Fatal(err)
		}
		if want := read(t, "userdata_"+name+".golden"); got != want {
			os.WriteFile(filepath.Join(os.TempDir(), "userdata_"+name+".got"), []byte(got), 0o600)
			t.Errorf("%s differs from the script's output:\n--- got\n%s\n--- want\n%s", name, tail(got), tail(want))
		}
	}
}

func tail(s string) string {
	if i := strings.Index(s, "\n    ssh_authorized_keys:"); i >= 0 {
		return s[i:]
	}
	return s
}

// cloud-init.yaml of the repository has exactly one place for the keys.
func TestTheRepositoriesCloudInitFitsAndKeepsItsBody(t *testing.T) {
	b, err := os.ReadFile("../../cloud-init.yaml")
	if err != nil {
		t.Skip("no cloud-init.yaml")
	}
	got, err := UserData(b, []string{"ssh-ed25519 AAAA k"}, "PRIV\n", "ssh-ed25519 PUB", "ssh-ed25519-cert-v01@openssh.com CERT")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "#cloud-config\n") || !strings.Contains(got, "    lock_passwd: true\n    ssh_authorized_keys:\n      - \"ssh-ed25519 AAAA k\"\n") {
		t.Errorf("keys not placed:\n%s", got[:400])
	}
	if strings.Contains(string(b), "ssh_keys:") {
		t.Error("cloud-init.yaml must not define ssh_keys itself")
	}
	if _, err := UserData([]byte("no user here\n"), nil, "", "", ""); err == nil {
		t.Error("a file without the place for the keys is refused")
	}
	if _, err := UserData([]byte(needle+needle), nil, "", "", ""); err == nil {
		t.Error("two places are ambiguous")
	}
}

// With python3 available, compare with the script's own code directly.
func TestUserDataAgreesWithPythonOnOddKeys(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	sh, err := os.ReadFile("../../start.sh")
	if err != nil {
		t.Skip("no start.sh")
	}
	m := regexp.MustCompile(`(?s)<<'PYGEN'\n(.*?)\nPYGEN\n`).FindSubmatch(sh)
	if m == nil {
		t.Skip("start.sh has no PYGEN any more")
	}
	dir := t.TempDir()
	write := func(n, c string) string { p := filepath.Join(dir, n); os.WriteFile(p, []byte(c), 0o600); return p }
	for _, key := range []string{"ssh-ed25519 AAAA literal \\u003cx and \\<y and <z> & w", "ssh-ed25519 AAAA del\x7fchar bell\a nul-free\v form\f back\b", "ssh-ed25519 AAAA line\u2028sep \u00a0nbsp",
		"ssh-ed25519 AAAA plain", "ssh-ed25519 AAAA \"q\" 'single' \\ back", "ssh-ed25519 AAAA ü €  😀 <tag> & more\t tab", "ssh-ed25519 AAAA\n"} {
		write("ssh_host_ed25519_key", "P1\nP2\n")
		write("ssh_host_ed25519_key.pub", "ssh-ed25519 PUB c\n")
		write("ssh_host_ed25519_key-cert.pub", "ssh-ed25519-cert-v01@openssh.com CERT c\n")
		login := write("login.pub", key)
		out := filepath.Join(dir, "out.yaml")
		cmd := exec.Command(py, "-", login, "../../cloud-init.yaml", out, dir, "")
		cmd.Stdin = strings.NewReader(string(m[1]))
		if o, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("python: %v\n%s", err, o)
		}
		want, _ := os.ReadFile(out)
		base, _ := os.ReadFile("../../cloud-init.yaml")
		got, err := UserData(base, []string{key}, "P1\nP2\n", "ssh-ed25519 PUB c\n", "ssh-ed25519-cert-v01@openssh.com CERT c\n")
		if err != nil || got != string(want) {
			t.Errorf("%q: differs (%v)\n--- got\n%s\n--- want\n%s", key, err, tail(got), tail(string(want)))
		}
	}
}

func TestScriptsAreTheOnesOfStartSh(t *testing.T) {
	sh, err := os.ReadFile("../../start.sh")
	if err != nil {
		t.Skip("no start.sh")
	}
	for tag, got := range map[string]string{"SWAP": swapScript, "GITID": gitIDScript, "CLONE": cloneScript, "BREWPKG": brewPkgScript, "CDLINE": cdLineScript, "INVENTORY": inventoryScript} {
		m := regexp.MustCompile(`(?s)<<'` + tag + `'[^\n]*\n(.*?)\n` + tag + `\n`).FindSubmatch(sh)
		if m == nil {
			t.Skipf("start.sh has no %s any more", tag)
		}
		if string(m[1])+"\n" != got {
			t.Errorf("internal/start/scripts for %s differs from the heredoc in start.sh", tag)
		}
	}
}

// ssh-keygen is called as the script called it; a fake records the arguments.
func TestMakeHostKeyAsksForACertificateOfTheDNSName(t *testing.T) {
	var calls [][]string
	run := func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		key := args[len(args)-1]
		if strings.HasSuffix(key, "ssh_host_ed25519_key") { // generation
			os.WriteFile(key, []byte("PRIVATE\n"), 0o600)
			return os.WriteFile(key+".pub", []byte("ssh-ed25519 AAAAhost claude-web\n"), 0o644)
		}
		return os.WriteFile(strings.TrimSuffix(key, ".pub")+"-cert.pub", []byte("ssh-ed25519-cert-v01@openssh.com AAAAcert claude-web-1\n"), 0o644)
	}
	h, err := MakeHostKey(run, "/home/me/.ssh/hetzner_claude_host_ca", "claude-web", "claude-web.example.com", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if h.Private != "PRIVATE\n" || !strings.Contains(h.Certificate, "AAAAcert") || h.KnownHostsLine != "claude-web.example.com ssh-ed25519 AAAAhost" {
		t.Errorf("%+v", h)
	}
	gen, sign := calls[0], calls[1]
	if strings.Join(gen[:9], " ") != "ssh-keygen -q -t ed25519 -N  -C claude-web -f" {
		t.Errorf("generation %q", gen)
	}
	if strings.Join(sign[:10], " ") != "ssh-keygen -q -s /home/me/.ssh/hetzner_claude_host_ca -I claude-web-1700000000 -h -n claude-web.example.com -V" || sign[10] != "-5m:+7d" {
		t.Errorf("signing %q", sign)
	}
}

func TestJSONStringIsPythonsJSONDumps(t *testing.T) {
	for in, want := range map[string]string{
		"plain": `"plain"`, `a"b\c`: `"a\"b\\c"`, "tab\tnl\ncr\r": `"tab\tnl\ncr\r"`, "\b\f": `"\b\f"`, "\x01\x1f": `"\u0001\u001f"`,
		"del\x7f": `"del\u007f"`, "<>&": `"<>&"`, `\u003c`: `"\\u003c"`, "ü€": `"\u00fc\u20ac"`, "😀": `"\ud83d\ude00"`, "": `""`, "~ ": `"~ "`,
	} {
		if got := jsonString(in); got != want {
			t.Errorf("%q: got %s, want %s", in, got, want)
		}
	}
}

// Odd keys, with the output of the script's own Python code (made once, python3 is not always here).
func TestUserDataOfOddKeysIsWhatPythonMade(t *testing.T) {
	base, err := os.ReadFile("testdata/cloud-init.yaml")
	if err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob("testdata/odd/key*.pub")
	if len(files) < 5 {
		t.Fatalf("only %d odd keys", len(files))
	}
	for _, f := range files {
		key, _ := os.ReadFile(f)
		want, _ := os.ReadFile(strings.TrimSuffix(f, ".pub") + ".golden")
		got, err := UserData(base, []string{string(key)}, "P1\nP2\n", "ssh-ed25519 PUB c\n", "ssh-ed25519-cert-v01@openssh.com CERT c\n")
		if err != nil || got != string(want) {
			t.Errorf("%s differs from Python's output (%v)\n--- got\n%s\n--- want\n%s", f, err, tail(got), tail(string(want)))
		}
	}
}
