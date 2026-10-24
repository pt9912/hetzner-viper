package check_test

import (
	"bytes"
	"context"
	"errors"
	"github.com/pt9912/hetzner-viper/internal/check"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/config"
	"github.com/pt9912/hetzner-viper/internal/hetzner"
)

func TestParseProbeKeepsTheFirstValue(t *testing.T) {
	got := check.ParseProbe("cloudinit=done\nclaude_answer=OK\nmem=1 MB available of 2 MB\nweird line\ncloudinit=other\nx=a=b\n")
	if got["cloudinit"] != "done" || got["claude_answer"] != "OK" || got["mem"] != "1 MB available of 2 MB" || got["x"] != "a=b" {
		t.Errorf("%q", got)
	}
}

func TestProbeIsAValidBashScript(t *testing.T) {
	if strings.TrimSpace(check.Probe()) == "" || !strings.HasPrefix(check.Probe(), "set -uo pipefail\n") {
		t.Error("probe not embedded")
	}
	_ = context.Background
}

type fixture struct {
	t        *testing.T
	home     string
	probeOut string
	sshFails bool
	devFails string // stderr of the "another device" ssh; "" = works
	doh      map[string]string
	server   *hetzner.Server
	calls    [][]string
	stdins   []string
	local    []string
}

func newFixture(t *testing.T, settings string) (*fixture, *config.Config) {
	t.Helper()
	f := &fixture{t: t, home: t.TempDir(), server: &hetzner.Server{ID: 1, Name: "claude-web", IPv4: "203.0.113.7"}, local: []string{"203.0.113.7"},
		probeOut: okProbe, doh: map[string]string{}}
	dir := t.TempDir()
	if err := os.MkdirAll(f.home+"/.ssh", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/.env.local", []byte("DNS_DOMAIN=example.com\n"+settings), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(dir, "web", config.Env{Home: f.home, Lookup: func(string) (string, bool) { return "", false }}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.SSHConfig, []byte("Host x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return f, c
}

const okProbe = `cloudinit=disabled
cloudinit_errors=0
claude_version=2.0.1 (Claude Code)
claude_auth=none
gh_version=gh version 2.60.0
gh_auth=no
git_name=Ada
git_email=ada@example.com
docker=ok
swap_mb=0
brew_version=Homebrew 5.0.0
brew_path=yes
brew_missing=
sshd=authenticationmethods publickey;passwordauthentication no;permitrootlogin no;
mem=900 MB available of 1900 MB
disk=30G free of 38G
`

func (f *fixture) deps() check.Deps {
	return check.Deps{
		Hetzner: serverFake{f},
		HTTP: &http.Client{Transport: rt(func(r *http.Request) (*http.Response, error) {
			key := r.URL.Host + " " + r.URL.Query().Get("type")
			body, ok := f.doh[key]
			if !ok {
				return nil, errors.New("offline")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
		})},
		LookupIPv4: func(context.Context, string) ([]string, error) { return f.local, nil },
		Nameserver: "192.168.1.1",
		Run: func(_ context.Context, stdin, name string, args ...string) (string, string, error) {
			f.calls = append(f.calls, append([]string{name}, args...))
			f.stdins = append(f.stdins, stdin)
			switch {
			case name == "ssh" && args[0] == "-n":
				if f.devFails != "" {
					return "", "warning\n" + f.devFails + "\n", errors.New("exit 255")
				}
				return "", "", nil
			case name == "ssh":
				if f.sshFails {
					return "", "", errors.New("exit 255")
				}
				return f.probeOut, "", nil
			}
			return "", "", errors.New("not available")
		},
		WriteSSHConfig: func(string) error { return nil },
	}
}

type serverFake struct{ f *fixture }

func (s serverFake) ServerByName(context.Context, string) (*hetzner.Server, error) {
	return s.f.server, nil
}

type rt func(*http.Request) (*http.Response, error)

func (f rt) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func dohAnswer(ips ...string) string {
	var recs []string
	for _, ip := range ips {
		recs = append(recs, `{"type":1,"data":"`+ip+`"}`)
	}
	return `{"Answer":[` + strings.Join(recs, ",") + `]}`
}

func (f *fixture) run(c *config.Config) (string, int, error) {
	f.t.Helper()
	var out, errb bytes.Buffer
	n, err := check.Run(context.Background(), c, f.deps(), &out, &errb)
	return out.String() + errb.String(), n, err
}

func contains(t *testing.T, out string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(out, p) {
			t.Errorf("missing %q in\n%s", p, out)
		}
	}
}

func TestEverythingFine(t *testing.T) {
	f, c := newFixture(t, "")
	f.doh["cloudflare-dns.com A"] = dohAnswer("203.0.113.7")
	f.doh["dns.google A"] = dohAnswer("203.0.113.7")
	out, n, err := f.run(c)
	if err != nil || n != 0 {
		t.Fatalf("%d problems, %v\n%s", n, err, out)
	}
	contains(t, out, "== DNS (claude-web.example.com)\n", "ok    Cloudflare 1.1.1.1 resolves to 203.0.113.7", "ok    Google 8.8.8.8 resolves to 203.0.113.7",
		"ok    this computer's resolver (192.168.1.1) resolves it to 203.0.113.7",
		"== SSH like another device (host CA, DNS name)\nok    connection to claude-web.example.com verified by the host CA",
		"info  host certificate validity could not be read",
		"== server (claude-web, 203.0.113.7)\n", "ok    cloud-init: done (disabled after the first boot)",
		"info  Claude Code 2.0.1 (Claude Code): no VIPER_CLAUDE_TOKEN or VIPER_ANTHROPIC_BASE_URL, log in with claude (/login)",
		"info  gh version 2.60.0: not logged in (no VIPER_GH_TOKEN)", "ok    git commits as Ada <ada@example.com>", "ok    docker running",
		"ok    Homebrew 5.0.0", "ok    sshd: public key only, no password, no root login", "info  memory: 900 MB available of 1900 MB; disk: 30G free of 38G",
		"\nCheck OK.\n")
	// the probe runs through ssh with the machine's config, the settings as quoted arguments
	var remote []string
	for i, c := range f.calls {
		if c[0] == "ssh" && c[1] == "-F" && c[2] != "/dev/null" {
			remote = c
			if !strings.HasPrefix(f.stdins[i], "set -uo pipefail") {
				t.Errorf("stdin %q", f.stdins[i])
			}
		}
	}
	if len(remote) != 5 || remote[4] != "bash -s -- '/Development' '' '/home/linuxbrew/.linuxbrew/bin/brew' '' '' '' '' '0' '8080'" {
		t.Errorf("remote call %q", remote)
	}
}

func TestDnsProblems(t *testing.T) {
	f, c := newFixture(t, "")
	f.doh["cloudflare-dns.com A"] = dohAnswer("198.51.100.1", "203.0.113.7")
	f.doh["dns.google A"] = dohAnswer("198.51.100.1")
	f.local = nil
	f.doh["cloudflare-dns.com SOA"] = `{"Authority":[{"type":6,"data":"ns.example.com. hostmaster. 1 7200 3600 1209600 1800"}]}`
	out, n, _ := f.run(c)
	contains(t, out, "ok    Cloudflare 1.1.1.1 resolves to 203.0.113.7", "FAIL  Google 8.8.8.8 resolves to '198.51.100.1', expected 203.0.113.7 (new record? wait a few minutes)",
		"info  this computer's resolver (192.168.1.1) resolves it to 'nothing'. If the public resolvers above are right, it still",
		"info    remembers 'no such name' from a lookup while the record did not exist, for up to 1800 s")
	if n != 1 {
		t.Errorf("%d problems", n)
	}
	f.doh = map[string]string{}
	f.local = []string{"198.51.100.9"}
	out, n, _ = f.run(c)
	contains(t, out, "WARN  Cloudflare 1.1.1.1 could not be asked", "WARN  Google 8.8.8.8 could not be asked", "resolves it to '198.51.100.9'", "for up to ? s")
	if n != 0 {
		t.Errorf("a resolver that cannot be asked is no problem: %d", n)
	}
}

func TestAnotherDeviceCannotConnect(t *testing.T) {
	f, c := newFixture(t, "")
	f.devFails = "Host key verification failed."
	out, n, _ := f.run(c)
	contains(t, out, "FAIL  connection to claude-web.example.com with the host CA failed: Host key verification failed.")
	if n != 1 {
		t.Errorf("%d", n)
	}
}

func TestSshToTheServerFails(t *testing.T) {
	f, c := newFixture(t, "")
	f.sshFails = true
	out, n, err := f.run(c)
	contains(t, out, "FAIL  SSH to hetzner-claude-web failed", "\nCheck: 1 problem(s).\n")
	if err == nil || n != 1 {
		t.Errorf("%d %v", n, err)
	}
	f.server = nil
	_, _, err = f.run(c)
	if err == nil {
		t.Error("no server")
	}
}

func TestClaudeCode(t *testing.T) {
	for _, c := range []struct {
		settings, probe string
		wantOK          bool
		want            string
	}{
		{"VIPER_CLAUDE_TOKEN=t\n", "claude_auth=token\nclaude_answer=OK\n", true, "ok    Claude Code 2.0.1 (Claude Code): logged in via VIPER_CLAUDE_TOKEN and answers"},
		{"VIPER_CLAUDE_TOKEN=t\n", "claude_auth=token\nclaude_answer=ok.\n", true, "and answers"},
		{"VIPER_CLAUDE_TOKEN=t\n", "claude_auth=token\nclaude_answer=Invalid API key. Please run /login OK\n", false, "FAIL  Claude Code 2.0.1 (Claude Code): no answer with the token: Invalid API key. Please run /login OK"},
		{"VIPER_CLAUDE_TOKEN=t\n", "claude_auth=none\n", false, "FAIL  Claude Code 2.0.1 (Claude Code): token not loaded in shells (rerun viper up web)"},
		{"VIPER_ANTHROPIC_BASE_URL=https://gw.example.com\nVIPER_CLAUDE_MODEL=glm\n", "claude_auth=gateway glm https://gw.example.com\nclaude_answer=OK\n", true, "ok    Claude Code 2.0.1 (Claude Code): model glm at https://gw.example.com answers"},
		{"VIPER_ANTHROPIC_BASE_URL=https://gw.example.com\nVIPER_CLAUDE_MODEL=glm\n", "claude_auth=gateway other https://gw.example.com\n", false, "FAIL  Claude Code 2.0.1 (Claude Code): not configured for glm at https://gw.example.com (has: gateway other https://gw.example.com; rerun viper up web)"},
		{"VIPER_ANTHROPIC_BASE_URL=https://u:s3cret@gw.example.com\nVIPER_CLAUDE_MODEL=glm\n", "claude_auth=gateway x https://u:s3cret@gw.example.com\n", false, "(has: gateway x https://***@gw.example.com;"},
		{"VIPER_ANTHROPIC_BASE_URL=https://gw.example.com\nVIPER_CLAUDE_MODEL=glm\n", "claude_auth=gateway glm https://gw.example.com\nclaude_answer=quota exceeded\n", false, "FAIL  Claude Code 2.0.1 (Claude Code): no answer from glm: quota exceeded"},
	} {
		f, cfg := newFixture(t, c.settings)
		f.probeOut = strings.ReplaceAll(okProbe, "claude_auth=none\n", "") + c.probe
		f.probeOut = c.probe + okProbe
		out, n, _ := f.run(cfg)
		contains(t, out, c.want)
		if strings.Contains(out, "s3cret") {
			t.Errorf("credentials shown:\n%s", out)
		}
		if gotOK := !strings.Contains(out, "FAIL  Claude Code"); gotOK != c.wantOK {
			t.Errorf("%q: ok=%v, n=%d\n%s", c.want, gotOK, n, out)
		}
	}
	f, cfg := newFixture(t, "")
	f.probeOut = strings.Replace(okProbe, "claude_version=2.0.1 (Claude Code)\n", "claude_version=\n", 1)
	out, _, _ := f.run(cfg)
	contains(t, out, "FAIL  Claude Code not installed")
}

func TestGithubGitDockerSwapAndFriends(t *testing.T) {
	f, c := newFixture(t, "VIPER_GH_TOKEN=x\nVIPER_SWAP=1\n")
	f.probeOut = "gh_auth=no\nswap_mb=0\ngit_name=\ndocker=no\n" + okProbe
	out, n, _ := f.run(c)
	contains(t, out, "FAIL  gh version 2.60.0: not logged in although VIPER_GH_TOKEN is set (rerun viper up web)",
		"WARN  git user.name/user.email are not set on the server: commits fail (VIPER_GIT_USER_NAME, VIPER_GIT_USER_EMAIL, then rerun viper up web)",
		"FAIL  docker not running", "FAIL  swap not active (VIPER_SWAP=1; rerun viper up web)")
	if n != 3 {
		t.Errorf("%d", n)
	}
	f.probeOut = "gh_auth=yes\nswap_mb=2048\n" + okProbe
	out, _, _ = f.run(c)
	contains(t, out, "ok    gh version 2.60.0: logged in", "ok    swap active (2048 MB)")
}

func TestCodeServer(t *testing.T) {
	f, c := newFixture(t, "VIPER_CODE_SERVER=1\nVIPER_CODE_SERVER_PASSWORD=pw-pw-pw-pw-pw\n")
	out, _, _ := f.run(c)
	contains(t, out, "FAIL  code-server not installed (rerun viper up web; log: ~/.config/viper/code-server-install.log)")
	f.probeOut = "code_server_version=4.9.0\ncode_server_active=inactive\ncode_server_http=no\n" + okProbe
	out, _, _ = f.run(c)
	contains(t, out, "FAIL  code-server 4.9.0 is not running (rerun viper up web; journalctl -u code-server@builder)")
	f.probeOut = "code_server_version=4.9.0\ncode_server_active=active\ncode_server_http=yes\n" + okProbe
	out, _, _ = f.run(c)
	contains(t, out, "ok    code-server 4.9.0 on 127.0.0.1:8080 (tunnel: ssh -L 8080:127.0.0.1:8080 hetzner-claude-web)")
	if strings.Contains(out, "pw-pw") {
		t.Error("password shown")
	}
}

func TestHomebrew(t *testing.T) {
	f, c := newFixture(t, "VIPER_BREW_TAPS=\"hashicorp/tap=https://u:tok@git.example.com/x\"\nVIPER_BREW_TRUST=hashicorp/tap/terraform\nVIPER_BREW_PACKAGES=\"ripgrep fd\"\nVIPER_BREWFILE=1\nREPO_URL=https://git.example.com/o/r.git\n")
	f.probeOut = "brew_missing=\nbrewfile=ok\nrepo=abc123 first commit\nrepo_changes=3\n" + okProbe
	out, n, _ := f.run(c)
	contains(t, out, "ok    Homebrew: taps, trust entries and formulae present (hashicorp/tap=https://***@git.example.com/x; trust hashicorp/tap/terraform; ripgrep fd)",
		"ok    Homebrew: Brewfile of the repository satisfied", "ok    repository /Development/r at abc123 first commit (3 uncommitted changes)")
	if strings.Contains(out, "tok@") || n != 0 {
		t.Errorf("%d\n%s", n, out)
	}
	f.probeOut = "brew_missing=ripgrep trust:x\nbrewfile=missing\n" + okProbe
	out, _, _ = f.run(c)
	contains(t, out, "FAIL  Homebrew: missing ripgrep trust:x (rerun viper up web; log: ~/.config/viper/brew-packages.log)", "WARN  Homebrew: Brewfile of the repository not fully installed")
	// remote call has the Brewfile path and the settings
	var remote string
	for _, cl := range f.calls {
		if cl[0] == "ssh" && cl[1] == "-F" && cl[2] != "/dev/null" {
			remote = cl[4]
		}
	}
	contains(t, remote, "'/Development/r/Brewfile'", "'hashicorp/tap=https://u:tok@git.example.com/x'", "'ripgrep fd'")
	f, c = newFixture(t, "")
	f.probeOut = strings.Replace(okProbe, "brew_version=Homebrew 5.0.0\n", "brew_version=\n", 1)
	out, _, _ = f.run(c)
	contains(t, out, "FAIL  Homebrew not installed (rerun viper up web; log: ~/.config/viper/homebrew-install.log)")
	f.probeOut = strings.Replace(okProbe, "brew_path=yes", "brew_path=no", 1)
	out, _, _ = f.run(c)
	contains(t, out, "FAIL  Homebrew 5.0.0 installed but not in the shells' PATH (rerun viper up web)")
	f, c = newFixture(t, "VIPER_HOMEBREW=0\n")
	f.probeOut = strings.Replace(okProbe, "brew_version=Homebrew 5.0.0\n", "brew_version=\n", 1)
	out, _, _ = f.run(c)
	if strings.Contains(out, "Homebrew") {
		t.Errorf("not checked when off:\n%s", out)
	}
}

func TestSshdRebootCloudInitAndRepository(t *testing.T) {
	f, c := newFixture(t, "REPO_URL=https://git.example.com/o/r.git\n")
	f.probeOut = "reboot=required\ncloudinit_errors=2\ncloudinit_error=Failed run of module\n" + strings.Replace(strings.Replace(okProbe, "passwordauthentication no", "passwordauthentication yes", 1), "cloudinit=disabled", "cloudinit=running", 1)
	out, n, _ := f.run(c)
	contains(t, out, "WARN  cloud-init: running", "WARN  cloud-init reported 2 error(s) at the first boot, e.g.: Failed run of module (see /var/log/cloud-init.log)",
		"FAIL  repository missing: /Development/r", "FAIL  sshd hardening not in effect: authenticationmethods publickey;passwordauthentication yes;permitrootlogin no;",
		"WARN  updates need a reboot: viper ssh web --cmd 'sudo reboot' (tmux sessions end)", "Check: 2 problem(s).")
	if n != 2 {
		t.Errorf("%d", n)
	}
	f.probeOut = strings.Replace(okProbe, "cloudinit=disabled\n", "cloudinit=\n", 1)
	out, _, _ = f.run(c)
	contains(t, out, "FAIL  cloud-init: no status")
	f.probeOut = strings.Replace(okProbe, "cloudinit=disabled", "cloudinit=done", 1)
	out, _, _ = f.run(c)
	contains(t, out, "ok    cloud-init: done\n")
}

func TestWritesTheSshConfigIfThereIsNone(t *testing.T) {
	f, c := newFixture(t, "")
	_ = os.Remove(c.SSHConfig) // may not exist
	var wrote string
	d := f.deps()
	d.WriteSSHConfig = func(ip string) error { wrote = ip; return nil }
	var out bytes.Buffer
	_, _ = check.Run(context.Background(), c, d, &out, &out)
	if wrote != "203.0.113.7" {
		t.Errorf("wrote %q", wrote)
	}
}
