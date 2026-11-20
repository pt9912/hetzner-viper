package natsmcp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/natsmcp"
)

func TestViaArgsAreReadAndRefused(t *testing.T) {
	abs := t.TempDir()
	o, err := natsmcp.ParseViaArgs([]string{"dev", "--viper", "/x/viper", "--dir", abs, "--push-from", "w3, w5", "--files", abs, "--send-per-min", "5", "--yes", "--name", "me"})
	if err != nil || o.Group != "dev" || o.Viper != "/x/viper" || o.Dir != abs || o.Files != abs || o.SendPerMin != 5 || !o.Yes || o.Name != "me" || len(o.PushFrom) != 2 || o.PushFrom[1] != "w5" {
		t.Errorf("%v %+v", err, o)
	}
	if o, err := natsmcp.ParseViaArgs([]string{"dev"}); err != nil || o.Name != "local" || o.SendPerMin != natsmcp.DefaultViaSendPerMin {
		t.Errorf("the defaults: %v %+v", err, o)
	}
	for name, args := range map[string][]string{
		"no group":         {},
		"two groups":       {"dev", "other"},
		"dash group":       {"-x"},
		"unknown option":   {"dev", "--shell", "sh"},
		"relative dir":     {"dev", "--dir", "rel"},
		"relative files":   {"dev", "--files", "rel"},
		"bad name":         {"dev", "--name", "A B"},
		"bad push-from":    {"dev", "--push-from", "a.b"},
		"bad count":        {"dev", "--send-per-min", "x"},
		"missing value":    {"dev", "--name"},
		"negative per-min": {"dev", "--send-per-min", "-1"},
	} {
		if _, err := natsmcp.ParseViaArgs(args); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// readyFixture writes the files a line ready names and returns the line as a map.
func readyFixture(t *testing.T) map[string]any {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []string{"ca.pem", "local.nk"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return map[string]any{"event": "ready", "version": "v1", "group": "dev", "name": "local", "url": "tls://127.0.0.1:41000", "server_name": "10.0.0.2",
		"ca_file": filepath.Join(dir, "ca.pem"), "seed_file": filepath.Join(dir, "local.nk"), "settings_dir": dir, "state_dir": dir, "objects_file_max": 1 << 20, "pid": 7, "future": "x"}
}

func parseReady(t *testing.T, m map[string]any) error {
	t.Helper()
	b, _ := json.Marshal(m)
	_, err := natsmcp.ParseReady(b, natsmcp.ViaOptions{Group: "dev", Name: "local"}, "v1")
	return err
}

func TestTheLineReadyIsCheckedLikeInputFromOutside(t *testing.T) {
	if err := parseReady(t, readyFixture(t)); err != nil {
		t.Fatalf("a good line (with a field that is not known): %v", err)
	}
	dir := t.TempDir()
	for name, mod := range map[string]func(m map[string]any){
		"another event":       func(m map[string]any) { m["event"] = "hello" },
		"another group":       func(m map[string]any) { m["group"] = "other" },
		"another name":        func(m map[string]any) { m["name"] = "other" },
		"another version":     func(m map[string]any) { m["version"] = "v2" },
		"a remote address":    func(m map[string]any) { m["url"] = "tls://10.0.0.2:4222" },
		"no port":             func(m map[string]any) { m["url"] = "tls://127.0.0.1" },
		"a port too high":     func(m map[string]any) { m["url"] = "tls://127.0.0.1:99999" },
		"no TLS but a CA":     func(m map[string]any) { m["url"] = "nats://127.0.0.1:41000" },
		"a relative CA":       func(m map[string]any) { m["ca_file"] = "ca.pem" },
		"a CA that is a dir":  func(m map[string]any) { m["ca_file"] = dir },
		"no seed":             func(m map[string]any) { m["seed_file"] = filepath.Join(dir, "none") },
		"a relative settings": func(m map[string]any) { m["settings_dir"] = "." },
		"a settings file":     func(m map[string]any) { m["settings_dir"] = m["seed_file"] },
		"a negative size":     func(m map[string]any) { m["objects_file_max"] = -1 },
		"a relative state":    func(m map[string]any) { m["state_dir"] = "state" },
	} {
		m := readyFixture(t)
		mod(m)
		if err := parseReady(t, m); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := natsmcp.ParseReady([]byte(strings.Repeat("x", 5000)), natsmcp.ViaOptions{}, "v1"); err == nil {
		t.Error("a line over 4 KiB")
	}
	if _, err := natsmcp.ParseReady([]byte("not json"), natsmcp.ViaOptions{}, "v1"); err == nil {
		t.Error("no JSON")
	}
	// without a certificate the address is nats://
	m := readyFixture(t)
	m["ca_file"], m["url"] = "", "nats://127.0.0.1:41000"
	if err := parseReady(t, m); err != nil {
		t.Errorf("a server without TLS: %v", err)
	}
}

func TestViperIsFoundByFlagNeverInTheCurrentFolder(t *testing.T) {
	dir := t.TempDir()
	prog := filepath.Join(dir, "viper")
	if err := os.WriteFile(prog, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := natsmcp.FindViper(prog); err != nil || got != prog {
		t.Errorf("%q %v", got, err)
	}
	if _, err := natsmcp.FindViper(dir); err == nil {
		t.Error("a folder is no program")
	}
	if _, err := natsmcp.FindViper(filepath.Join(dir, "none")); err == nil {
		t.Error("a file that is not there")
	}
	// a viper in the current folder is not taken, also when PATH names the current folder
	t.Chdir(dir)
	t.Setenv("PATH", ".")
	if got, err := natsmcp.FindViper(""); err == nil && !strings.HasSuffix(got, "viper") {
		t.Errorf("%q", got)
	} else if err == nil && filepath.Dir(got) == dir {
		t.Errorf("the viper of the current folder was taken: %s", got)
	}
}

func TestOnlyWhatTheTunnelNeedsGoesToTheChild(t *testing.T) {
	env := natsmcp.ChildEnv([]string{"HOME=/h", "PATH=/p", "SSH_AUTH_SOCK=/s", "XDG_STATE_HOME=/x", "VIPER_TEST=1", "HCLOUD_TOKEN=secret", "ANTHROPIC_API_KEY=k", "AWS_SECRET_ACCESS_KEY=a"})
	got := strings.Join(env, " ")
	for _, want := range []string{"HOME=/h", "PATH=/p", "SSH_AUTH_SOCK=/s", "XDG_STATE_HOME=/x", "VIPER_TEST=1"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s is missing: %s", want, got)
		}
	}
	for _, bad := range []string{"HCLOUD_TOKEN", "ANTHROPIC", "AWS_"} {
		if strings.Contains(got, bad) {
			t.Errorf("%s goes to the child: %s", bad, got)
		}
	}
}

func TestDefaultConfigHasTheLimitsAnInboxNeeds(t *testing.T) {
	c := natsmcp.DefaultConfig(natsmcp.Names{Group: "g", Me: "m"}, "/seed")
	if c.InboxMax == 0 || c.InboxPerMan == 0 || c.PushPerMin == 0 || c.MaxText == 0 || c.PushFrom == nil || c.SeedFile != "/seed" {
		t.Errorf("%+v", c)
	}
}

func TestACertificateThatChangedIsNoticed(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a, a2, b := write("a.pem", "cert A"), write("a2.pem", "cert A"), write("b.pem", "cert B")
	if natsmcp.CertificateChanged(a, a2) {
		t.Error("the same certificate in another folder is no change")
	}
	if !natsmcp.CertificateChanged(a, b) {
		t.Error("another certificate")
	}
	if !natsmcp.CertificateChanged(a, filepath.Join(dir, "none")) {
		t.Error("a certificate that cannot be read counts as changed: the connection must not go on unchecked")
	}
	if natsmcp.CertificateChanged("", "") {
		t.Error("a server without TLS has none to change")
	}
}
