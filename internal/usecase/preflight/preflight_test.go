package preflight_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/hetzner"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/settingsfile"
	"github.com/pt9912/hetzner-viper/internal/model"
	portsettings "github.com/pt9912/hetzner-viper/internal/port/driven/settings"
	"github.com/pt9912/hetzner-viper/internal/servertest"
	"github.com/pt9912/hetzner-viper/internal/usecase/preflight"
)

const pubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGFiY2RlZmdoaWprbG1ub3BxcnN0dXZ3eHl6MDEyMzQ1 test\n"

type fakeHetzner struct {
	servers   []string
	types     map[string]*hetzner.ServerType
	locations map[string]bool
	images    map[string]bool // "name/arch"
	keys      []hetzner.SSHKey
	apiErr    error
}

func (f *fakeHetzner) ServerNames(context.Context) ([]string, error) { return f.servers, f.apiErr }
func (f *fakeHetzner) ServerType(_ context.Context, n string) (*hetzner.ServerType, error) {
	return f.types[n], nil
}
func (f *fakeHetzner) LocationExists(_ context.Context, n string) (bool, error) {
	return f.locations[n], nil
}
func (f *fakeHetzner) ImageExists(_ context.Context, n, a string) (bool, error) {
	return f.images[n+"/"+a], nil
}
func (f *fakeHetzner) SSHKeys(context.Context) ([]hetzner.SSHKey, error) { return f.keys, nil }

func defaultHetzner() *fakeHetzner {
	loc := func(avail ...string) []hetzner.ServerTypeLocation {
		var out []hetzner.ServerTypeLocation
		for _, n := range avail {
			out = append(out, hetzner.ServerTypeLocation{Name: n, Available: true})
		}
		return out
	}
	return &fakeHetzner{
		types: map[string]*hetzner.ServerType{
			"cpx32": {Name: "cpx32", Cores: 4, MemoryGB: 8, DiskGB: 160, Architecture: "x86", Locations: loc("fsn1", "nbg1", "hel1")},
			"cpx02": {Name: "cpx02", Cores: 2, MemoryGB: 4, DiskGB: 80, Architecture: "x86", Locations: loc("fsn1", "nbg1")},
			"cax11": {Name: "cax11", Cores: 2, MemoryGB: 4, DiskGB: 40, Architecture: "arm", Locations: loc("fsn1")},
		},
		locations: map[string]bool{"fsn1": true, "nbg1": true, "hel1": true},
		images:    map[string]bool{"ubuntu-26.04/x86": true, "ubuntu-26.04/arm": true},
	}
}

type fakeDNS struct {
	lines []string
	ok    bool
	err   error
}

func (f fakeDNS) Check(context.Context, string, string) ([]string, bool, error) {
	return f.lines, f.ok, f.err
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func reply(code int, body string, hdr map[string]string) *http.Response {
	h := http.Header{}
	for k, v := range hdr {
		h.Set(k, v)
	}
	return &http.Response{StatusCode: code, Header: h, Body: io.NopCloser(strings.NewReader(body))}
}

type env struct {
	t    *testing.T
	home string
	dir  string
	h    *fakeHetzner
	dns  fakeDNS
	http roundTrip
	git  func(context.Context, string) error
	reqs []*http.Request
}

// fixture: a settings directory and a home with login key and host CA, like after viper setup.
func fixture(t *testing.T, settings string) *env {
	t.Helper()
	e := &env{t: t, home: t.TempDir(), dir: t.TempDir(), h: defaultHetzner(), dns: fakeDNS{ok: true, lines: []string{"ok    Cloudflare zone example.com readable (edit permission is only seen when viper up writes the record)", "ok    claude-web.example.com: no record yet"}}}
	ssh := filepath.Join(e.home, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ssh, "hetzner-build"), []byte("PRIVATE"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ssh, "hetzner-build.pub"), []byte(pubKey), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ssh, "hetzner_claude_host_ca"), []byte("PRIVATE"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ssh, "known_hosts_hetzner_claude_ca"), []byte("@cert-authority *.example.com ssh-ed25519 AAAA\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.dir, ".env.local"), []byte("DNS_DOMAIN=example.com\n"+settings), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.dir, "machines"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.http = func(r *http.Request) (*http.Response, error) { return reply(404, "", nil), nil }
	return e
}

func (e *env) machine(name, settings string) {
	if err := os.WriteFile(filepath.Join(e.dir, "machines", name+".env.local"), []byte(settings), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) config(machine string) *model.Machine {
	e.t.Helper()
	c, err := settingsfile.Load(e.dir, machine, portsettings.Env{Home: e.home, Lookup: func(k string) (string, bool) {
		v, ok := map[string]string{"HCLOUD_TOKEN": "h", "CLOUDFLARE_API_TOKEN": "c", "CLOUDFLARE_ZONE_ID": "z"}[k]
		return v, ok
	}}, true)
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

// run returns the output, the errors and the number of problems.
func (e *env) run(machine, repo string) (string, string, int) {
	e.t.Helper()
	var out, errb bytes.Buffer
	d := preflight.Deps{Cloud: hetzner.Validator{API: e.h}, DNS: e.dns, GitLsRemote: e.git, HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		e.reqs = append(e.reqs, r)
		return e.http(r)
	})}}
	n := preflight.Run(context.Background(), e.config(machine), repo, d, &out, &errb)
	return out.String(), errb.String(), n
}

func mustContain(t *testing.T, out string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(out, p) {
			t.Errorf("missing %q in\n%s", p, out)
		}
	}
}

func mustNotContain(t *testing.T, out string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(out, p) {
			t.Errorf("unexpected %q in\n%s", p, out)
		}
	}
}

func TestEverythingFineAndReadOnly(t *testing.T) {
	e := fixture(t, "")
	out, errb, n := e.run("web", "")
	if n != 0 || errb != "" {
		t.Fatalf("%d problems: %s %s", n, out, errb)
	}
	mustContain(t, out,
		"== settings and local setup (claude-web)\n", "ok    settings complete: cpx32 in fsn1, ubuntu-26.04, DNS claude-web.example.com",
		"ok    login key "+filepath.Join(e.home, ".ssh", "hetzner-build")+" and host CA present", "ok    host CA trusted for *.example.com",
		"ok    SSH_ALLOWED_CIDRS: 0.0.0.0/0,::/0", "== Hetzner Cloud\n",
		"ok    server type cpx32 exists (4 vCPU, 8 GB RAM, 160 GB disk), architecture x86", "ok    location fsn1 exists",
		"ok    cpx32 can currently be ordered in fsn1", "ok    image ubuntu-26.04 exists for x86",
		"info  login key will be uploaded as claude-login", "info  server claude-web will be created",
		"== Cloudflare DNS\n", "== GitHub\ninfo  no VIPER_GH_TOKEN: gh is not logged in on the server",
		"info  no VIPER_GIT_USER_NAME / VIPER_GIT_USER_EMAIL: git commits on the server fail until user.name and user.email are set",
		"== Claude Code\ninfo  no VIPER_CLAUDE_TOKEN or VIPER_ANTHROPIC_BASE_URL",
		"info  Homebrew will be installed on the server", "== repository\ninfo  no repository to clone", "\nPreflight OK.\n")
}

func TestMissingLoginKeyAndCATrust(t *testing.T) {
	e := fixture(t, "")
	_ = os.Remove(filepath.Join(e.home, ".ssh", "hetzner-build.pub")) // may not exist
	if err := os.WriteFile(filepath.Join(e.home, ".ssh", "known_hosts_hetzner_claude_ca"), []byte("@cert-authority *.other.org AAAA\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, n := e.run("web", "")
	mustContain(t, out, "FAIL  login key or host CA missing: Run viper setup first", "FAIL  host CA trust for *.example.com missing", "Preflight: 2 problem(s); nothing was created.")
	if n != 2 {
		t.Errorf("%d problems", n)
	}
}

func TestServerTypeLocationAndImage(t *testing.T) {
	e := fixture(t, "")
	e.machine("a", "SERVER_TYPE=cx11\n")
	e.machine("b", "SERVER_LOCATION=xyz1\n")
	e.machine("c", "SERVER_TYPE=cax11\nSERVER_IMAGE=ubuntu-22.04\n")
	e.machine("d", "SERVER_TYPE=cpx02\nSERVER_LOCATION=hel1\n")
	a, _, _ := e.run("a", "")
	b, _, _ := e.run("b", "")
	c, _, _ := e.run("c", "")
	d, _, _ := e.run("d", "")
	mustContain(t, a, "FAIL  server type cx11 does not exist")
	mustContain(t, b, "FAIL  location xyz1 does not exist")
	mustContain(t, c, "FAIL  image ubuntu-22.04 does not exist for arm")
	mustContain(t, d, "FAIL  cpx02 is not offered in hel1 (available in: fsn1, nbg1); set SERVER_LOCATION or SERVER_TYPE, e.g. in machines/d.env.local")
}

func TestSoldOutAndDeprecatedTypes(t *testing.T) {
	e := fixture(t, "")
	e.machine("web", "SERVER_TYPE=cpx02\n")
	tp := e.h.types["cpx02"]
	tp.Locations = []hetzner.ServerTypeLocation{{Name: "fsn1", Available: false}, {Name: "nbg1", Available: true}}
	out, _, n := e.run("web", "")
	mustContain(t, out, "FAIL  cpx02 cannot currently be ordered in fsn1 (available in: nbg1)")
	if n != 1 {
		t.Errorf("%d problems", n)
	}
	tp.Locations = []hetzner.ServerTypeLocation{{Name: "fsn1", Available: true, UnavailableAfter: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)}}
	out, _, n = e.run("web", "")
	mustContain(t, out, "WARN  cpx02 is deprecated in fsn1 and unavailable after 2027-01-01T00:00:00Z")
	if n != 0 {
		t.Errorf("a deprecation only warns: %d", n)
	}
	tp.Locations = []hetzner.ServerTypeLocation{{Name: "nbg1", Available: false}}
	out, _, _ = e.run("web", "")
	mustContain(t, out, "(available in: none)")
}

func TestHetznerApiProblem(t *testing.T) {
	e := fixture(t, "")
	e.h.apiErr = errors.New("hcloud: server responded with status code 401")
	out, _, n := e.run("web", "")
	mustContain(t, out, "FAIL  Hetzner API not usable (token, project, network): unauthorized (check HCLOUD_TOKEN)")
	mustNotContain(t, out, "server type")
	if n != 1 {
		t.Errorf("%d problems", n)
	}
}

func TestLoginKeyAlreadyInTheProject(t *testing.T) {
	e := fixture(t, "")
	fp := servertest.FingerprintFile(filepath.Join(e.home, ".ssh", "hetzner-build.pub"))
	if len(fp) != 47 {
		t.Fatalf("fingerprint %q", fp)
	}
	e.h.keys = []hetzner.SSHKey{{Name: "my laptop key", Fingerprint: strings.ToUpper(fp)}}
	e.h.servers = []string{"claude-web", "other"}
	out, _, n := e.run("web", "")
	mustContain(t, out, `ok    login key already in the project as "my laptop key" (reused)`, "info  server claude-web exists; viper up reuses it")
	if n != 0 {
		t.Errorf("%d", n)
	}
	e.h.keys = []hetzner.SSHKey{{Name: "claude-login", Fingerprint: "00:11"}}
	out, _, n = e.run("web", "")
	mustContain(t, out, "FAIL  SSH key claude-login in Hetzner belongs to a different public key than "+filepath.Join(e.home, ".ssh", "hetzner-build.pub"))
	if n != 1 {
		t.Errorf("%d", n)
	}
}

func TestDnsProblemsAreCounted(t *testing.T) {
	e := fixture(t, "")
	e.dns = fakeDNS{ok: false, lines: []string{"FAIL  claude-web.example.com already has a record not managed by this setup"}}
	out, _, n := e.run("web", "")
	mustContain(t, out, "FAIL  claude-web.example.com already has a record not managed by this setup")
	if n != 1 {
		t.Errorf("%d", n)
	}
	e.dns = fakeDNS{err: errors.New("Cloudflare lookup of claude-web.example.com failed (x)")}
	_, errb, n := e.run("web", "")
	mustContain(t, errb, "Cloudflare lookup of claude-web.example.com failed")
	if n != 1 {
		t.Errorf("%d", n)
	}
}

func TestBadAllowedCidrs(t *testing.T) {
	e := fixture(t, "SSH_ALLOWED_CIDRS=10.0.0.0/8,anywhere,2001:db8::/32,\n")
	out, _, n := e.run("web", "")
	mustContain(t, out, "FAIL  SSH_ALLOWED_CIDRS: not a network: anywhere")
	mustNotContain(t, out, "ok    SSH_ALLOWED_CIDRS")
	if n != 1 {
		t.Errorf("%d", n)
	}
}

func TestReadableConfigWithTokensIsWarnedAbout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes do not exist on Windows")
	}
	e := fixture(t, "")
	if err := os.Chmod(filepath.Join(e.dir, ".env.local"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.machine("web", "SERVER_TYPE=cpx32\n")
	if err := os.Chmod(filepath.Join(e.dir, "machines", "web.env.local"), 0o640); err != nil {
		t.Fatal(err)
	}
	out, _, n := e.run("web", "")
	mustContain(t, out, "WARN  .env.local is readable by other users; it may hold tokens: chmod 600 .env.local",
		"WARN  "+filepath.Join("machines", "web.env.local")+" is readable by other users")
	if n != 0 {
		t.Errorf("a warning is no problem: %d", n)
	}
	if err := os.Chmod(filepath.Join(e.dir, ".env.local"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(e.dir, "machines", "web.env.local"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, _ = e.run("web", "")
	mustNotContain(t, out, "WARN")
}

func TestKeyFilesOfTheSettings(t *testing.T) {
	e := fixture(t, "")
	priv := filepath.Join(e.home, ".ssh", "ipad")
	if err := os.WriteFile(priv, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(e.home, ".ssh", "ipad.pub")
	if err := os.WriteFile(good, []byte(pubKey), 0o644); err != nil {
		t.Fatal(err)
	}
	e.machine("a", "IPAD_SSH_PUBLIC_KEY_FILE="+filepath.ToSlash(priv)+"\n") // a \ in .env.local is an escape
	e.machine("b", "IPAD_SSH_PUBLIC_KEY_FILE=~/.ssh/ipad.pub\nGIT_DEPLOY_KEY_FILE="+filepath.ToSlash(priv)+"\n")
	e.machine("c", "IPAD_SSH_PUBLIC_KEY_FILE=/nonexistent/key.pub\n")
	e.machine("d", "IPAD_SSH_PUBLIC_KEY_FILE="+filepath.ToSlash(good)+"\n")
	if err := os.WriteFile(filepath.Join(e.home, ".ssh", "garbage.pub"), []byte("not a key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.machine("e", "IPAD_SSH_PUBLIC_KEY_FILE=~/.ssh/garbage.pub\n")
	a, _, _ := e.run("a", "")
	b, _, _ := e.run("b", "")
	c, _, _ := e.run("c", "")
	d, _, _ := e.run("d", "")
	f, _, _ := e.run("e", "")
	mustContain(t, a, "FAIL  IPAD_SSH_PUBLIC_KEY_FILE must be a public key (.pub), not a private key: "+filepath.ToSlash(priv))
	mustContain(t, b, "ok    IPAD_SSH_PUBLIC_KEY_FILE readable", "ok    GIT_DEPLOY_KEY_FILE readable")
	mustContain(t, c, "FAIL  IPAD_SSH_PUBLIC_KEY_FILE: File not readable: /nonexistent/key.pub")
	mustContain(t, d, "ok    IPAD_SSH_PUBLIC_KEY_FILE readable")
	mustContain(t, f, "FAIL  IPAD_SSH_PUBLIC_KEY_FILE must be a public key")
}

func TestSshConfigNotWritableWarns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes do not exist on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root can write anything")
	}
	e := fixture(t, "")
	cfg := filepath.Join(e.home, ".ssh", "config")
	if err := os.WriteFile(cfg, []byte("Host x\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	out, _, n := e.run("web", "")
	mustContain(t, out, "WARN  ~/.ssh/config ("+cfg+") is not writable: viper up cannot add 'Include "+filepath.Join(e.home, ".ssh", "config_hetzner_claude-web")+"'")
	if n != 0 {
		t.Errorf("%d", n)
	}
}

func writeFile(p, s string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(s), 0o600)
}

func TestWithoutDNSThePreflightSaysSo(t *testing.T) {
	e := fixture(t, "DNS_PROVIDER=none\n")
	e.dns = fakeDNS{ok: true, lines: []string{"ok    DNS_PROVIDER=none: no DNS record is made, the server is reached by its address"}}
	out, _, n := e.run("web", "")
	if n != 0 {
		t.Fatalf("problems: %d\n%s", n, out)
	}
	mustContain(t, out, "== DNS (none)\nok    DNS_PROVIDER=none")
	if strings.Contains(out, "== Cloudflare DNS") {
		t.Errorf("no Cloudflare without a DNS name:\n%s", out)
	}
}
