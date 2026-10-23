package integration

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/check"
	"github.com/pt9912/hetzner-viper/internal/config"
	"github.com/pt9912/hetzner-viper/internal/hetzner"
	"github.com/pt9912/hetzner-viper/internal/machine"
	"github.com/pt9912/hetzner-viper/internal/preflight"
	"github.com/pt9912/hetzner-viper/internal/runlog"
	"github.com/pt9912/hetzner-viper/internal/servertest"
	"github.com/pt9912/hetzner-viper/internal/start"
	"github.com/pt9912/hetzner-viper/internal/stop"
)

var now = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

// Env is a computer with a settings directory and a home, a cloud and a server.
type Env struct {
	t     *testing.T
	Dir   string
	Home  string
	Srv   *servertest.Server
	Cloud *servertest.Cloud
	// Tokens are the variables of the environment the commands see.
	Tokens map[string]string
	// HTTP answers the requests of the preflight (GitHub, the Claude gateway).
	HTTP func(*http.Request) (*http.Response, error)
	// VSCode records what was opened.
	VSCode []string
	// Sleeps are the pauses that were asked for (they do not wait).
	Sleeps []time.Duration
	// Answers are the typed answers of down, in order.
	Answers []string
	// LocalIP is what this computer's resolver says for the DNS name ("" = the server's IP).
	LocalIP string
}

func tool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("no %s", name)
	}
}

// New makes a computer like after viper setup: login key, host CA, trust for the domain, and a
// .env.local with the given settings added to DNS_DOMAIN, the work directory and Homebrew's place.
func New(t *testing.T, settings string) *Env {
	t.Helper()
	for _, p := range []string{"ssh-keygen", "bash", "git"} {
		tool(t, p)
	}
	root := t.TempDir()
	brew := filepath.Join(root, "linuxbrew", "bin", "brew")
	e := &Env{t: t, Dir: filepath.Join(root, "settings"), Home: filepath.Join(root, "home"), Srv: servertest.New(t, brew),
		Cloud: servertest.NewCloud(now), Tokens: map[string]string{"HCLOUD_TOKEN": "h", "CLOUDFLARE_API_TOKEN": "c", "CLOUDFLARE_ZONE_ID": "z"}}
	ssh := filepath.Join(e.Home, ".ssh")
	for _, d := range []string{ssh, filepath.Join(e.Dir, "machines")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	key, ca := filepath.Join(ssh, "hetzner-build"), filepath.Join(ssh, "hetzner_claude_host_ca")
	for _, k := range [][]string{{"-t", "ed25519", "-N", "", "-f", key, "-q"}, {"-t", "ed25519", "-N", "", "-f", ca, "-C", "Hetzner Claude Host CA", "-q"}} {
		if out, err := exec.Command("ssh-keygen", k...).CombinedOutput(); err != nil {
			t.Skipf("ssh-keygen does not run here: %v %s", err, out)
		}
	}
	pub, _ := os.ReadFile(ca + ".pub")
	os.WriteFile(filepath.Join(ssh, "known_hosts_hetzner_claude_ca"), []byte("@cert-authority *.example.com "+strings.TrimSpace(string(pub))+"\n"), 0o600)
	e.WriteSettings(settings)
	e.HTTP = defaultHTTP
	return e
}

// WriteSettings replaces .env.local: the defaults of the test plus these settings.
func (e *Env) WriteSettings(settings string) {
	e.t.Helper()
	body := "DNS_DOMAIN=example.com\nVIPER_BREW=" + e.Srv.BrewPath + "\nWORKSPACE_DIR=" + filepath.Join(filepath.Dir(e.Dir), "mnt") + "\n" + settings
	if err := os.WriteFile(filepath.Join(e.Dir, ".env.local"), []byte(body), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

// Machine writes machines/<name>.env.local.
func (e *Env) Machine(name, settings string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.Dir, "machines", name+".env.local"), []byte(settings), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

// Reply is an HTTP answer.
func Reply(code int, body string, hdr map[string]string) *http.Response {
	h := http.Header{}
	for k, v := range hdr {
		h.Set(k, v)
	}
	return &http.Response{StatusCode: code, Header: h, Body: io.NopCloser(strings.NewReader(body))}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func (e *Env) client() *http.Client {
	return &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		// DNS over HTTPS: both public resolvers say what the cloud has
		if strings.Contains(r.URL.Host, "dns") {
			host := r.URL.Query().Get("name")
			var data string
			for name, srv := range e.Cloud.Servers {
				if strings.HasPrefix(host, name+".") {
					data = srv.IPv4
				}
			}
			body := `{"Answer":[]}`
			if data != "" {
				body = `{"Answer":[{"type":1,"data":"` + data + `"}]}`
			}
			return Reply(200, body, nil), nil
		}
		return e.HTTP(r)
	})}
}

// Config is the machine's configuration as the commands read it.
func (e *Env) Config(name string) *config.Config {
	e.t.Helper()
	c, err := config.Load(e.Dir, name, config.Env{Home: e.Home, Lookup: func(k string) (string, bool) { v, ok := e.Tokens[k]; return v, ok }}, true)
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *Env) preflightDeps() preflight.Deps {
	return preflight.Deps{Hetzner: e.Cloud, DNS: e.Cloud, HTTP: e.client(), GitLsRemote: func(ctx context.Context, url string) error {
		cmd := exec.CommandContext(ctx, "git", "ls-remote", "--", url)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		return cmd.Run()
	}}
}

// Result of a command: the exit code and everything it printed (stdout and stderr).
type Result struct {
	Code   int
	Out    string
	Stdout string
	Stderr string
	Err    error
}

func (r Result) Has(t *testing.T, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(r.Out, p) {
			t.Errorf("missing %q in\n%s", p, r.Out)
		}
	}
}

func (r Result) HasNot(t *testing.T, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(r.Out, p) {
			t.Errorf("unexpected %q in\n%s", p, r.Out)
		}
	}
}

func open(e *Env, c *config.Config, action string, out, errb *bytes.Buffer) *runlog.Log {
	e.t.Helper()
	l, err := runlog.Open(c.StateDir, c.ServerName, action, out, errb)
	if err != nil {
		e.t.Fatal(err)
	}
	return l
}

func result(code int, err error, out, errb *bytes.Buffer) Result {
	return Result{Code: code, Out: out.String() + errb.String(), Stdout: out.String(), Stderr: errb.String(), Err: err}
}

// Preflight runs viper preflight.
func (e *Env) Preflight(name, repo string) Result {
	e.t.Helper()
	c := e.Config(name)
	var out, errb bytes.Buffer
	l := open(e, c, "preflight", &out, &errb)
	n := preflight.Run(context.Background(), c, repo, e.preflightDeps(), l.Out, l.Err)
	code := 0
	if n > 0 {
		l.Final("preflight: %d problem(s)", n)
		code = 1
	} else {
		l.Final("preflight OK")
	}
	l.Close(nil, code)
	return result(code, nil, &out, &errb)
}

// Up runs viper up.
func (e *Env) Up(name, repo string) Result {
	e.t.Helper()
	c := e.Config(name)
	var out, errb bytes.Buffer
	l := open(e, c, "up", &out, &errb)
	d := start.Deps{
		Preflight: func(ctx context.Context, repoArg string, o, eo io.Writer) int {
			return preflight.Run(ctx, c, repoArg, e.preflightDeps(), o, eo)
		},
		Hetzner: e.Cloud, DNS: e.Cloud, Run: e.Srv.Run, Stream: e.Srv.Stream,
		Keygen: func(name string, args ...string) error {
			if o, err := exec.Command(name, args...).CombinedOutput(); err != nil {
				return &toolError{err, string(o)}
			}
			return nil
		},
		WriteSSHConfig: func(ip string) error { return machine.WriteSSHConfig(c, ip) },
		SSHInclude:     func(a string) error { return machine.SSHInclude(c, a) },
		Sleep:          func(d time.Duration) { e.Sleeps = append(e.Sleeps, d) },
		Now:            func() time.Time { return now },
		OpenVSCode:     func(alias, dir string) { e.VSCode = append(e.VSCode, alias+" "+dir) },
	}
	code, err := start.Run(context.Background(), c, repo, d, l, l.Out, l.Err)
	l.Close(err, code)
	if s := e.Cloud.Servers[c.ServerName]; s != nil {
		e.Srv.UserData = e.Cloud.UserData[c.ServerName]
	}
	return result(code, err, &out, &errb)
}

type toolError struct {
	err error
	out string
}

func (e *toolError) Error() string { return e.err.Error() + ": " + e.out }

// Check runs viper check.
func (e *Env) Check(name string) Result {
	e.t.Helper()
	c := e.Config(name)
	var out, errb bytes.Buffer
	l := open(e, c, "check", &out, &errb)
	local := e.LocalIP
	d := check.Deps{
		Hetzner: e.Cloud, HTTP: e.client(),
		LookupIPv4: func(_ context.Context, host string) ([]string, error) {
			if local != "" {
				return []string{local}, nil
			}
			if s := e.Cloud.Servers[c.ServerName]; s != nil {
				return []string{s.IPv4}, nil
			}
			return nil, nil
		},
		Run:            e.Srv.Run,
		WriteSSHConfig: func(ip string) error { return machine.WriteSSHConfig(c, ip) },
	}
	n, err := check.Run(context.Background(), c, d, l.Out, l.Err)
	code := 0
	switch {
	case err != nil:
		l.Close(err, 1)
		return result(1, err, &out, &errb)
	case n > 0:
		l.Final("check: %d problem(s)", n)
		code = 1
	default:
		l.Final("check OK")
	}
	l.Close(nil, code)
	return result(code, nil, &out, &errb)
}

// Down runs viper down with the answers.
func (e *Env) Down(name string, force bool, answers ...string) Result {
	e.t.Helper()
	c := e.Config(name)
	var out, errb bytes.Buffer
	l := open(e, c, "down", &out, &errb)
	queue := append([]string(nil), answers...)
	d := stop.Deps{
		Hetzner: e.Cloud, DNS: e.Cloud, Run: e.Srv.Run,
		Ask: func(prompt string) string {
			l.Err.Write([]byte(prompt))
			if len(queue) == 0 {
				return ""
			}
			a := queue[0]
			queue = queue[1:]
			return a
		},
		WriteSSHConfig: func(ip string) error { return machine.WriteSSHConfig(c, ip) },
		SSHInclude:     func(a string) error { return machine.SSHInclude(c, a) },
	}
	code, err := stop.Run(context.Background(), c, force, d, l, l.Out, l.Err)
	l.Close(err, code)
	return result(code, err, &out, &errb)
}

// Events is events.log of the computer.
func (e *Env) Events() string {
	b, _ := os.ReadFile(filepath.Join(e.Home, ".local", "state", "hetzner-claude", "events.log"))
	return string(b)
}

// Log is the transcript of the machine.
func (e *Env) Log(server string) string {
	b, _ := os.ReadFile(filepath.Join(e.Home, ".local", "state", "hetzner-claude", "logs", server+".log"))
	return string(b)
}

// MustUp runs up and fails the test if it does not succeed.
func (e *Env) MustUp(name, repo string) Result {
	e.t.Helper()
	r := e.Up(name, repo)
	if r.Code != 0 || r.Err != nil {
		e.t.Fatalf("up: %d %v\n%s", r.Code, r.Err, r.Out)
	}
	return r
}

// Home file of the machine (the server's sandbox home).
func (e *Env) ServerFile(rel string) string {
	b, _ := os.ReadFile(filepath.Join(e.Srv.Home, rel))
	return string(b)
}

func gitCmd(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// GitHub makes the preflight see GitHub (api.github.com and github.com) with a token for the
// user "tester". scopes is the header of a classic token ("" = a fine-grained one).
func (e *Env) GitHub(scopes string) {
	e.HTTP = func(r *http.Request) (*http.Response, error) {
		switch {
		case r.URL.Host == "api.github.com" && r.URL.Path == "/user":
			h := map[string]string{}
			if scopes != "" {
				h["X-OAuth-Scopes"] = scopes
			}
			return Reply(200, `{"login":"tester"}`, h), nil
		case r.URL.Host == "api.github.com" && strings.HasPrefix(r.URL.Path, "/repos/"):
			return Reply(200, `{}`, nil), nil
		case r.URL.Host == "github.com" && strings.HasSuffix(r.URL.Path, ".git/info/refs"):
			return Reply(200, ``, nil), nil
		}
		return Reply(404, ``, nil), nil
	}
}

// Gateway makes llm.test.invalid answer like an Anthropic-compatible API: Bearer token olk_secret_9,
// the models listed (a model that is not listed is "not found").
func (e *Env) Gateway(models ...string) {
	e.HTTP = func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "llm.test.invalid" || r.URL.Path != "/v1/messages" {
			return Reply(404, "", nil), nil
		}
		if r.Header.Get("Authorization") != "Bearer olk_secret_9" {
			return Reply(401, `{"error":{"message":"invalid api key"}}`, nil), nil
		}
		b, _ := io.ReadAll(r.Body)
		for _, m := range models {
			if strings.Contains(string(b), `"model":"`+m+`"`) {
				return Reply(200, `{}`, nil), nil
			}
		}
		return Reply(404, `{"error":{"message":"model not found"}}`, nil), nil
	}
}

// RemoteEnv is the value of a variable in a non-interactive shell on the server ("<unset>" if none).
func (e *Env) RemoteEnv(name string) string {
	e.t.Helper()
	cmd := exec.Command("bash", "-c", `. ~/.bashrc >/dev/null 2>&1; printf %s "${`+name+`-<unset>}"`)
	cmd.Env = []string{"HOME=" + e.Srv.Home, "PATH=" + os.Getenv("PATH")}
	out, _ := cmd.Output()
	return string(out)
}

// GitConfig is a value of the global git config on the server.
func (e *Env) GitConfig(key string) string {
	cmd := exec.Command("git", "config", "--file", filepath.Join(e.Srv.Home, ".gitconfig"), key)
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out))
}

// defaultHTTP is the world outside as the tests found it: GitHub knows the user "tester" (a classic
// token with the scopes gh needs), and a gateway at llm.test.invalid answers every request that
// has a bearer token.
func defaultHTTP(r *http.Request) (*http.Response, error) {
	switch {
	case r.URL.Host == "api.github.com" && r.URL.Path == "/user":
		return Reply(200, `{"login":"tester"}`, map[string]string{"X-OAuth-Scopes": "repo, read:org"}), nil
	case r.URL.Host == "api.github.com" && strings.HasPrefix(r.URL.Path, "/repos/"):
		return Reply(200, `{}`, nil), nil
	case r.URL.Host == "github.com" && strings.HasSuffix(r.URL.Path, ".git/info/refs"):
		return Reply(200, ``, nil), nil
	case r.URL.Host == "llm.test.invalid" && r.URL.Path == "/v1/messages":
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			return Reply(401, `{"error":{"message":"invalid api key"}}`, nil), nil
		}
		return Reply(200, `{}`, nil), nil
	}
	return Reply(404, ``, nil), nil
}

type firewallRule = hetzner.FirewallRule

func servertestRecord(ip string, ours bool) servertest.DNSRecord {
	return servertest.DNSRecord{IP: ip, Ours: ours}
}
