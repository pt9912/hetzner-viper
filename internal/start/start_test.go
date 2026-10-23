package start

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/config"
	"github.com/pt9912/hetzner-viper/internal/hetzner"
	"github.com/pt9912/hetzner-viper/internal/preflight"
	"github.com/pt9912/hetzner-viper/internal/runlog"
)

const pubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGFiY2RlZmdoaWprbG1ub3BxcnN0dXZ3eHl6MDEyMzQ1 me@host\n"

type sshCall struct {
	args  []string
	stdin string
}

func (c sshCall) command() string { return c.args[len(c.args)-1] }

type world struct {
	t   *testing.T
	c   *config.Config
	lg  *runlog.Log
	out bytes.Buffer
	err bytes.Buffer

	preflightProblems int
	keys              []hetzner.SSHKey
	keyCreated        []string
	firewall          *[]hetzner.FirewallRule // nil: no firewall
	firewallCreated   bool
	addedRules        []hetzner.FirewallRule
	server            *hetzner.Server // existing server, nil: none
	created           []hetzner.ServerSpec
	createdServer     hetzner.Server
	userData          string
	listErr           error
	dnsSet            []string
	dnsErr            error

	probes    []func() (string, string, error) // answers of the wait loop, the last one repeats
	probeN    int
	sleeps    []time.Duration
	calls     []sshCall
	failing   map[string]error // substring of the remote command -> error
	hostKey   string
	now       time.Time
	includes  []string
	vscode    []string
	keygenRun [][]string
}

const okStatus = "cloud-init=0\nclaude=ok\ndocker=ok\nready=\n"

func newWorld(t *testing.T, settings string) *world {
	t.Helper()
	home, dir := t.TempDir(), t.TempDir()
	ssh := filepath.Join(home, ".ssh")
	os.MkdirAll(ssh, 0o700)
	os.WriteFile(filepath.Join(ssh, "hetzner-build"), []byte("PRIVATE"), 0o600)
	os.WriteFile(filepath.Join(ssh, "hetzner-build.pub"), []byte(pubKey), 0o644)
	os.WriteFile(filepath.Join(ssh, "hetzner_claude_host_ca"), []byte("CA"), 0o600)
	os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\n"+settings), 0o600)
	os.MkdirAll(filepath.Join(dir, "machines"), 0o755)
	c, err := config.Load(dir, "web", config.Env{Home: home, Lookup: func(string) (string, bool) { return "", false }}, false)
	if err != nil {
		t.Fatal(err)
	}
	lg, err := runlog.Open(c.StateDir, c.ServerName, "up", io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, c: c, lg: lg, failing: map[string]error{}, hostKey: "ssh-ed25519 AAAAhostkey root@server\n",
		now: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}
	w.probes = []func() (string, string, error){func() (string, string, error) { return okStatus, "", nil }}
	w.createdServer = hetzner.Server{ID: 42, Name: "claude-web", IPv4: "203.0.113.7", Created: w.now}
	return w
}

type hz struct{ w *world }

func (h hz) ServerByName(context.Context, string) (*hetzner.Server, error) {
	return h.w.server, h.w.listErr
}
func (h hz) SSHKeys(context.Context) ([]hetzner.SSHKey, error) { return h.w.keys, nil }
func (h hz) SSHKeyCreate(_ context.Context, name, pub string) error {
	h.w.keyCreated = append(h.w.keyCreated, name+"|"+strings.TrimSpace(pub))
	return nil
}
func (h hz) FirewallRules(context.Context, string) ([]hetzner.FirewallRule, bool, error) {
	if h.w.firewall == nil {
		return nil, false, nil
	}
	return *h.w.firewall, true, nil
}
func (h hz) FirewallCreate(context.Context, string) error {
	h.w.firewallCreated = true
	empty := []hetzner.FirewallRule{}
	h.w.firewall = &empty
	return nil
}
func (h hz) FirewallAddRule(_ context.Context, _ string, r hetzner.FirewallRule) error {
	h.w.addedRules = append(h.w.addedRules, r)
	return nil
}
func (h hz) ServerCreate(_ context.Context, s hetzner.ServerSpec) (*hetzner.Server, error) {
	h.w.created = append(h.w.created, s)
	h.w.userData = s.UserData
	srv := h.w.createdServer
	return &srv, nil
}

type dnsFake struct{ w *world }

func (d dnsFake) Set(_ context.Context, host, ip string) (string, error) {
	d.w.dnsSet = append(d.w.dnsSet, host+" "+ip)
	if d.w.dnsErr != nil {
		return "", d.w.dnsErr
	}
	return host + " -> " + ip, nil
}

func (w *world) deps() Deps {
	return Deps{
		Preflight: func(_ context.Context, _ string, out, _ io.Writer) int {
			if w.preflightProblems > 0 {
				io.WriteString(out, "FAIL  something\n\nPreflight: 1 problem(s); nothing was created.\n")
			} else {
				io.WriteString(out, "== settings\nPreflight OK.\n")
			}
			return w.preflightProblems
		},
		Hetzner: hz{w}, DNS: dnsFake{w},
		Run: func(_ context.Context, stdin, name string, args ...string) (string, string, error) {
			last := args[len(args)-1]
			switch {
			case name == "ssh" && last == probe:
				i := w.probeN
				if i >= len(w.probes) {
					i = len(w.probes) - 1
				}
				w.probeN++
				return w.probes[i]()
			case name == "ssh" && strings.HasPrefix(last, "cat /etc/ssh/ssh_host_ed25519_key.pub"):
				return w.hostKey, "", nil
			}
			return "", "", errors.New("unexpected call: " + last)
		},
		Stream: func(_ context.Context, stdin string, stdout, stderr io.Writer, name string, args ...string) error {
			w.calls = append(w.calls, sshCall{append([]string{name}, args...), stdin})
			cmd := args[len(args)-1]
			for sub, err := range w.failing {
				if strings.Contains(cmd, sub) || (stdin != "" && strings.Contains(stdin, sub)) {
					return err
				}
			}
			return nil
		},
		Keygen: func(name string, args ...string) error {
			w.keygenRun = append(w.keygenRun, append([]string{name}, args...))
			key := args[len(args)-1]
			if strings.HasSuffix(key, "ssh_host_ed25519_key") {
				os.WriteFile(key, []byte("HOSTPRIVATE\n"), 0o600)
				return os.WriteFile(key+".pub", []byte("ssh-ed25519 AAAAnewhost claude-web\n"), 0o644)
			}
			return os.WriteFile(strings.TrimSuffix(key, ".pub")+"-cert.pub", []byte("ssh-ed25519-cert-v01@openssh.com AAAAcert claude-web-1\n"), 0o644)
		},
		WriteSSHConfig: func(ip string) error { return os.WriteFile(w.c.SSHConfig, []byte("Host x\nHostName "+ip+"\n"), 0o600) },
		SSHInclude:     func(a string) error { w.includes = append(w.includes, a); return nil },
		Sleep:          func(d time.Duration) { w.sleeps = append(w.sleeps, d) },
		Now:            func() time.Time { return w.now },
		OpenVSCode:     func(alias, dir string) { w.vscode = append(w.vscode, alias+" "+dir) },
	}
}

func (w *world) run(repo string) (int, error) {
	w.t.Helper()
	code, err := Run(context.Background(), w.c, repo, w.deps(), w.lg, &w.out, &w.err)
	w.lg.Close(err, code)
	return code, err
}

func (w *world) events() string {
	b, _ := os.ReadFile(w.c.EventsLog)
	return string(b)
}

func (w *world) all() string { return w.out.String() + w.err.String() }

func (w *world) remote(sub string) []sshCall {
	var out []sshCall
	for _, c := range w.calls {
		if strings.Contains(c.command(), sub) {
			out = append(out, c)
		}
	}
	return out
}

func has(t *testing.T, s string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(s, p) {
			t.Errorf("missing %q in\n%s", p, s)
		}
	}
}

func hasNot(t *testing.T, s string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(s, p) {
			t.Errorf("unexpected %q in\n%s", p, s)
		}
	}
}

func TestCreatesAllResourcesForAMachine(t *testing.T) {
	w := newWorld(t, "")
	code, err := w.run("")
	if code != 0 || err != nil {
		t.Fatalf("%d %v\n%s", code, err, w.all())
	}
	fp := preflight.FingerprintMD5(w.c.SSHKey + ".pub")
	_ = fp
	if len(w.keyCreated) != 1 || w.keyCreated[0] != "claude-login|"+strings.TrimSpace(pubKey) {
		t.Errorf("key %q", w.keyCreated)
	}
	if !w.firewallCreated || len(w.addedRules) != 2 {
		t.Fatalf("firewall created %v, rules %v", w.firewallCreated, w.addedRules)
	}
	if r := w.addedRules[0]; r.Direction != "in" || r.Protocol != "tcp" || r.Port != "22" || strings.Join(r.SourceIPs, ",") != "0.0.0.0/0,::/0" {
		t.Errorf("ssh rule %+v", r)
	}
	if r := w.addedRules[1]; r.Protocol != "icmp" || r.Port != "" {
		t.Errorf("icmp rule %+v", r)
	}
	if len(w.created) != 1 {
		t.Fatalf("servers created: %d", len(w.created))
	}
	s := w.created[0]
	if s.Name != "claude-web" || s.Type != "cpx32" || s.Location != "fsn1" || s.Image != "ubuntu-26.04" || s.SSHKey != "claude-login" || s.Firewall != "claude-ssh" {
		t.Errorf("spec %+v", s)
	}
	has(t, w.userData, "#cloud-config", "ssh_authorized_keys:\n      - \""+strings.TrimSpace(pubKey)+"\"\n", "ssh_keys:\n  ed25519_private: |\n    HOSTPRIVATE\n",
		`ed25519_public: "ssh-ed25519 AAAAnewhost claude-web"`, `ed25519_certificate: "ssh-ed25519-cert-v01@openssh.com AAAAcert claude-web-1"`)
	hasNot(t, w.userData, "me@host\\n", "PRIVATE\n    LINE")
	if pin, _ := os.ReadFile(w.c.KnownPin); string(pin) != "claude-web.example.com ssh-ed25519 AAAAnewhost\n" {
		t.Errorf("pin %q", pin)
	}
	if st, _ := os.Stat(w.c.KnownPin); st.Mode().Perm() != 0o600 {
		t.Errorf("pin mode %v", st.Mode())
	}
	if len(w.dnsSet) != 1 || w.dnsSet[0] != "claude-web.example.com 203.0.113.7" {
		t.Errorf("dns %q", w.dnsSet)
	}
	if strings.Join(w.includes, ",") != "add" {
		t.Errorf("includes %v", w.includes)
	}
	for f, want := range map[string]string{w.c.IDState: "42\n", w.c.IPState: "203.0.113.7\n", w.c.ReadyState: "42\n", w.c.WorkspaceState: "/Development\n"} {
		if b, _ := os.ReadFile(f); string(b) != want {
			t.Errorf("%s = %q, want %q", filepath.Base(f), b, want)
		}
	}
	has(t, w.out.String(), "Preflight OK.", "SSH key claude-login uploaded.", "Firewall claude-ssh created.", "Server claude-web created.", "claude-web.example.com -> 203.0.113.7",
		"Waiting for SSH and cloud-init ...", "== installed on the server ==", "Ready: ssh hetzner-claude-web (work dir: /Development)")
	ev := w.events()
	has(t, ev, " up        start (cpx32, fsn1, ubuntu-26.04)\n", " up        server created id=42\n", " up        ip=203.0.113.7 dns=claude-web.example.com\n", " up        ready (work dir /Development)\n")
	if strings.Contains(ev, "FAILED") {
		t.Errorf("events %s", ev)
	}
	// the host key is signed for the DNS name with the CA
	var sign []string
	for _, k := range w.keygenRun {
		if k[1] == "-q" && k[2] == "-s" {
			sign = k
		}
	}
	if sign == nil || sign[3] != filepath.Join(w.c.SSHDir, "hetzner_claude_host_ca") || sign[8] != "claude-web.example.com" {
		t.Errorf("signing %q", sign)
	}
	if len(w.vscode) != 1 || w.vscode[0] != "hetzner-claude-web /Development" {
		t.Errorf("vscode %q", w.vscode)
	}
}

func TestAProblemInThePreflightStopsBeforeAnythingIsCreated(t *testing.T) {
	w := newWorld(t, "")
	w.preflightProblems = 1
	code, err := w.run("")
	if code != 1 || !errors.Is(err, ErrShown) {
		t.Fatalf("%d %v", code, err)
	}
	has(t, w.err.String(), "FAIL  something", "Preflight: 1 problem(s); nothing was created.")
	if len(w.keyCreated)+len(w.created)+len(w.dnsSet)+len(w.calls) != 0 || w.firewallCreated {
		t.Error("something was created")
	}
	has(t, w.events(), "start (cpx32")
}

func TestExistingKeyIsReusedUnderItsNameAndFirewallRulesAreKept(t *testing.T) {
	w := newWorld(t, "")
	w.keys = []hetzner.SSHKey{{Name: "my laptop key", Fingerprint: strings.ToUpper(preflight.FingerprintMD5(w.c.SSHKey + ".pub"))}}
	rules := []hetzner.FirewallRule{{Direction: "in", Protocol: "tcp", Port: "22", SourceIPs: []string{"10.0.0.0/8"}}, {Direction: "in", Protocol: "icmp"}}
	w.firewall = &rules
	if code, err := w.run(""); code != 0 || err != nil {
		t.Fatalf("%d %v\n%s", code, err, w.all())
	}
	if len(w.keyCreated) != 0 || w.created[0].SSHKey != "my laptop key" {
		t.Errorf("key created %q, server key %q", w.keyCreated, w.created[0].SSHKey)
	}
	if w.firewallCreated || len(w.addedRules) != 0 {
		t.Errorf("firewall touched: %v %v", w.firewallCreated, w.addedRules)
	}
	has(t, w.out.String(), "Existing firewall claude-ssh: verify that its rules permit your connection.")
}

func TestExistingFirewallWithOnlySshGetsIcmp(t *testing.T) {
	w := newWorld(t, "SSH_ALLOWED_CIDRS=10.0.0.0/8,192.0.2.0/24\n")
	rules := []hetzner.FirewallRule{{Direction: "in", Protocol: "tcp", Port: "22"}}
	w.firewall = &rules
	w.run("")
	if len(w.addedRules) != 1 || w.addedRules[0].Protocol != "icmp" || strings.Join(w.addedRules[0].SourceIPs, ",") != "10.0.0.0/8,192.0.2.0/24" {
		t.Errorf("added %+v", w.addedRules)
	}
	hasNot(t, w.out.String(), "verify that its rules permit")
}

func TestKeyNameTakenByAnotherPublicKeyFails(t *testing.T) {
	w := newWorld(t, "")
	w.keys = []hetzner.SSHKey{{Name: "claude-login", Fingerprint: "00:11:22"}}
	code, err := w.run("")
	if code != 1 || !errors.Is(err, ErrShown) || len(w.created) != 0 {
		t.Fatalf("%d %v", code, err)
	}
	has(t, w.err.String(), "SSH key claude-login in Hetzner belongs to a different public key than "+w.c.SSHKey+".pub")
}

func TestHcloudErrorAbortsBeforeCreating(t *testing.T) {
	w := newWorld(t, "")
	w.listErr = errors.New("hcloud: server responded with status code 401")
	code, err := w.run("")
	if code != 1 || err == nil || len(w.created) != 0 {
		t.Fatalf("%d %v", code, err)
	}
	has(t, w.err.String(), "hcloud server list failed (check HCLOUD_TOKEN / project / network): unauthorized")
	has(t, w.events(), "FAILED (exit 1")
}

func TestMissingDeployKeyFailsBeforeCreatingAnything(t *testing.T) {
	w := newWorld(t, "GIT_DEPLOY_KEY_FILE=/nonexistent/key\n")
	code, _ := w.run("https://git.example.com/o/r.git")
	if code != 1 || len(w.keyCreated)+len(w.created) != 0 || w.firewallCreated {
		t.Fatalf("%d", code)
	}
	has(t, w.err.String(), "File not readable: /nonexistent/key")
}

func TestARestartOfARunningMachineIsIdempotent(t *testing.T) {
	w := newWorld(t, "")
	w.server = &hetzner.Server{ID: 42, Name: "claude-web", IPv4: "203.0.113.7", Created: w.now.Add(-time.Hour)}
	rules := []hetzner.FirewallRule{{Direction: "in", Protocol: "tcp", Port: "22"}, {Direction: "in", Protocol: "icmp"}}
	w.firewall = &rules
	w.keys = []hetzner.SSHKey{{Name: "claude-login", Fingerprint: preflight.FingerprintMD5(w.c.SSHKey + ".pub")}}
	os.WriteFile(w.c.IDState, []byte("42\n"), 0o600)
	if code, err := w.run(""); code != 0 || err != nil {
		t.Fatalf("%d %v\n%s", code, err, w.all())
	}
	if len(w.created)+len(w.keyCreated)+len(w.addedRules) != 0 || w.firewallCreated || len(w.keygenRun) != 0 {
		t.Error("something was created on a restart")
	}
	has(t, w.out.String(), "Existing server: reusing it.")
	has(t, w.events(), " up        reusing server id=42\n")
	if len(w.dnsSet) != 1 {
		t.Errorf("dns %q", w.dnsSet)
	}
}

// --- waiting for the server

func TestSshNeverReachableFails(t *testing.T) {
	w := newWorld(t, "")
	w.probes = []func() (string, string, error){func() (string, string, error) {
		return "", "ssh: connect to host 203.0.113.7 port 22: Connection timed out\n", errors.New("exit 255")
	}}
	code, err := w.run("")
	if code != 1 || !errors.Is(err, ErrShown) {
		t.Fatalf("%d %v", code, err)
	}
	has(t, w.err.String(), "SSH not reachable; server retained. Last error: ssh: connect to host 203.0.113.7 port 22: Connection timed out")
	if w.probeN != 120 || len(w.sleeps) != 120 || w.sleeps[0] != 5*time.Second {
		t.Errorf("%d attempts, %d sleeps", w.probeN, len(w.sleeps))
	}
	if b, _ := os.ReadFile(w.c.ReadyState); len(b) != 0 {
		t.Error("an unreachable server must not count as set up")
	}
}

func TestATransientHostKeyErrorOfABootingServerIsRetried(t *testing.T) {
	w := newWorld(t, "")
	w.probes = []func() (string, string, error){
		func() (string, string, error) { return "", "Host key verification failed.\n", errors.New("exit 255") },
		func() (string, string, error) {
			return "", "builder@x: Permission denied (publickey).\n", errors.New("exit 255")
		},
		func() (string, string, error) { return okStatus, "", nil },
	}
	if code, err := w.run(""); code != 0 || err != nil {
		t.Fatalf("%d %v\n%s", code, err, w.all())
	}
	if w.probeN != 3 {
		t.Errorf("%d attempts", w.probeN)
	}
}

func TestAReusedServerWithAnUntrustedKeyFailsFast(t *testing.T) {
	w := newWorld(t, "")
	w.server = &hetzner.Server{ID: 42, Name: "claude-web", IPv4: "203.0.113.7", Created: w.now.Add(-time.Hour)}
	w.probes = []func() (string, string, error){func() (string, string, error) {
		return "", "Host key verification failed.\n", errors.New("exit 255")
	}}
	code, err := w.run("")
	if code != 1 || !errors.Is(err, ErrShown) || w.probeN != 1 || len(w.sleeps) != 0 {
		t.Fatalf("%d %v attempts %d", code, err, w.probeN)
	}
	has(t, w.err.String(), "SSH: the server's host key is not trusted; server retained.", "  Host key verification failed.",
		"The server answers but does not prove to be claude-web (stale pin, expired certificate without pin, or someone in between).")
	hasNot(t, w.err.String(), "not reachable")
	// the login key rejected
	w = newWorld(t, "")
	w.server = &hetzner.Server{ID: 42, Name: "claude-web", IPv4: "203.0.113.7", Created: w.now.Add(-time.Hour)}
	w.probes = []func() (string, string, error){func() (string, string, error) {
		return "", "builder@203.0.113.7: Permission denied (publickey).\n", errors.New("exit 255")
	}}
	w.run("")
	has(t, w.err.String(), "SSH: the server rejects the login key "+w.c.SSHKey+" (builder@203.0.113.7: Permission denied (publickey).); server retained.",
		"Was the server created with another login key?")
}

func TestANewServerWaitsForTheRebootAfterUpdates(t *testing.T) {
	w := newWorld(t, "")
	w.probes = []func() (string, string, error){
		func() (string, string, error) { return okStatus + "reboot=pending\n", "", nil },
		func() (string, string, error) { return okStatus + "reboot=pending\n", "", nil },
		func() (string, string, error) { return okStatus, "", nil },
	}
	if code, err := w.run(""); code != 0 || err != nil {
		t.Fatalf("%d %v", code, err)
	}
	has(t, w.out.String(), "Updates need a reboot; waiting for the server to restart ...")
	if strings.Count(w.out.String(), "Updates need a reboot") != 1 || w.probeN != 3 {
		t.Errorf("announced once, 3 attempts: %d", w.probeN)
	}
	hasNot(t, w.err.String(), "WARNING: updates on the server need a reboot")
}

func TestTheWaitForAPendingRebootIsCapped(t *testing.T) {
	w := newWorld(t, "")
	w.probes = []func() (string, string, error){func() (string, string, error) { return okStatus + "reboot=pending\n", "", nil }}
	if code, err := w.run(""); code != 0 || err != nil {
		t.Fatalf("%d %v", code, err)
	}
	if w.probeN != 37 {
		t.Errorf("%d attempts, want 37 (36 waits, then on)", w.probeN)
	}
	has(t, w.err.String(), "WARNING: updates on the server need a reboot: viper ssh web --cmd 'sudo reboot'")
}

func TestAReusedServerNeedingARebootOnlyWarns(t *testing.T) {
	w := newWorld(t, "")
	w.server = &hetzner.Server{ID: 42, Name: "claude-web", IPv4: "203.0.113.7", Created: w.now.Add(-time.Hour)}
	w.probes = []func() (string, string, error){func() (string, string, error) { return okStatus + "reboot=pending\n", "", nil }}
	w.run("")
	if w.probeN != 1 {
		t.Errorf("%d attempts", w.probeN)
	}
	has(t, w.err.String(), "WARNING: updates on the server need a reboot")
}

func TestAnInterruptedFirstRunIsNotASetUpServer(t *testing.T) {
	// a young server whose ready marker is missing waits for the reboot, an old one or one the
	// server itself marked ready does not
	young := func() *world {
		w := newWorld(t, "")
		w.server = &hetzner.Server{ID: 42, Name: "claude-web", IPv4: "203.0.113.7", Created: w.now.Add(-5 * time.Minute)}
		w.probes = []func() (string, string, error){func() (string, string, error) { return okStatus + "reboot=pending\n", "", nil }}
		return w
	}
	w := young()
	w.run("")
	if w.probeN != 37 {
		t.Errorf("young server without marker: %d attempts", w.probeN)
	}
	w = young()
	os.WriteFile(w.c.ReadyState, []byte("42\n"), 0o600)
	w.run("")
	if w.probeN != 1 {
		t.Errorf("marker on this computer: %d attempts", w.probeN)
	}
	w = young()
	w.probes = []func() (string, string, error){func() (string, string, error) { return "reboot=pending\nready=42\n" + okStatus, "", nil }}
	w.run("")
	if w.probeN != 1 {
		t.Errorf("marker on the server: %d attempts", w.probeN)
	}
}

func TestAMissingPinIsRecordedAfterTheHostCAVerifiedTheConnection(t *testing.T) {
	w := newWorld(t, "")
	w.server = &hetzner.Server{ID: 42, Name: "claude-web", IPv4: "203.0.113.7", Created: w.now.Add(-time.Hour)}
	w.run("")
	if pin, _ := os.ReadFile(w.c.KnownPin); string(pin) != "claude-web.example.com ssh-ed25519 AAAAhostkey\n" {
		t.Errorf("pin %q", pin)
	}
	has(t, w.out.String(), "Pinned host key of claude-web (verified via host CA).")
	// the stale pin of an earlier server is dropped
	w = newWorld(t, "")
	w.server = &hetzner.Server{ID: 42, Name: "claude-web", IPv4: "203.0.113.7", Created: w.now.Add(-time.Hour)}
	os.WriteFile(w.c.KnownPin, []byte("old pin\n"), 0o600)
	os.WriteFile(w.c.IDState, []byte("41\n"), 0o600)
	w.run("")
	has(t, w.out.String(), "Server claude-web was recreated (ID 41 -> 42); dropping its old pinned host key.")
}

// --- setting the server up

func TestClaudeCodeIsInstalledIfTheServerHasNone(t *testing.T) {
	w := newWorld(t, "")
	w.probes = []func() (string, string, error){func() (string, string, error) { return "cloud-init=0\ndocker=ok\nready=\n", "", nil }}
	if code, err := w.run(""); code != 0 || err != nil {
		t.Fatalf("%d %v", code, err)
	}
	has(t, w.out.String(), "Claude Code is not installed yet; installing ...")
	if c := w.remote("claude.ai/install.sh"); len(c) != 1 || c[0].command() != "bash -o pipefail -c 'curl -fsSL https://claude.ai/install.sh | bash' && sudo touch /var/tmp/claude-ready" || c[0].args[1] != "-n" {
		t.Errorf("install calls %v", c)
	}
}

func TestClaudeInstallIsRetriedWhenRateLimitedAndFailureIsReported(t *testing.T) {
	w := newWorld(t, "")
	w.probes = []func() (string, string, error){func() (string, string, error) { return "cloud-init=0\ndocker=ok\nready=\n", "", nil }}
	n := 0
	w.failing["claude.ai/install.sh"] = errors.New("exit 22")
	_ = n
	code, err := w.run("")
	if code != 1 || !errors.Is(err, ErrShown) {
		t.Fatalf("%d %v", code, err)
	}
	if len(w.remote("claude.ai/install.sh")) != 5 {
		t.Errorf("%d attempts", len(w.remote("claude.ai/install.sh")))
	}
	has(t, w.err.String(), "Claude Code installation failed (attempt 1 of 5); retrying in 30 s ...", "(attempt 4 of 5); retrying in 120 s ...",
		"Claude Code installation failed; server retained. Rerun viper up web to try again (a rate limit, HTTP 429, passes after a few minutes).")
	hasNot(t, w.err.String(), "(attempt 5 of 5)")
	want := []time.Duration{30, 60, 90, 120}
	var got []time.Duration
	for _, s := range w.sleeps {
		if s >= 30*time.Second {
			got = append(got, s/time.Second)
		}
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pauses %v", got)
		}
	}
}

func TestDockerNotRunningFailsAndCloudInitErrorsOnlyWarn(t *testing.T) {
	w := newWorld(t, "")
	w.probes = []func() (string, string, error){func() (string, string, error) { return "cloud-init=0\nclaude=ok\nready=\n", "", nil }}
	code, err := w.run("")
	if code != 1 || !errors.Is(err, ErrShown) {
		t.Fatalf("%d %v", code, err)
	}
	has(t, w.err.String(), "Docker is not running; see /var/log/cloud-init-output.log on the server. Server retained.")
	w = newWorld(t, "")
	w.probes = []func() (string, string, error){func() (string, string, error) { return "cloud-init=1\nclaude=ok\ndocker=ok\nready=\n", "", nil }}
	if code, _ := w.run(""); code != 0 {
		t.Fatalf("%d", code)
	}
	has(t, w.err.String(), "WARNING: cloud-init reported errors; see /var/log/cloud-init-output.log on the server.")
}

func TestWorkspaceIsRecordedOnTheServerBeforeLaterSteps(t *testing.T) {
	w := newWorld(t, "WORKSPACE_DIR=\"/srv/it's here\"\nVIPER_GH_TOKEN=x\n")
	w.failing["gh auth login"] = errors.New("exit 1")
	code, _ := w.run("")
	if code != 1 {
		t.Fatalf("%d", code)
	}
	c := w.remote("sudo install -d -o builder -g builder")
	if len(c) != 1 {
		t.Fatalf("workspace calls %d", len(c))
	}
	q := `'/srv/it'\''s here'`
	want := "sudo install -d -o builder -g builder " + q + " && mkdir -p ~/.config/viper && { grep -qxF -- " + q + " ~/.config/viper/workspaces 2>/dev/null || printf '%s\\n' " + q + " >> ~/.config/viper/workspaces; }"
	if c[0].command() != want {
		t.Errorf("command\n%s\nwant\n%s", c[0].command(), want)
	}
	if b, _ := os.ReadFile(w.c.WorkspaceState); string(b) != "/srv/it's here\n" {
		t.Errorf("recorded %q", b)
	}
	// a second run records it only once
	w2 := newWorld(t, "")
	os.WriteFile(w2.c.WorkspaceState, []byte("/Development\n"), 0o600)
	w2.server = &hetzner.Server{ID: 42, Name: "claude-web", IPv4: "203.0.113.7", Created: w2.now.Add(-time.Hour)}
	w2.run("")
	if b, _ := os.ReadFile(w2.c.WorkspaceState); string(b) != "/Development\n" {
		t.Errorf("recorded twice: %q", b)
	}
}

func TestGithubTokenGoesThroughStdinBeforeTheClone(t *testing.T) {
	w := newWorld(t, "VIPER_GH_TOKEN=ghp_secret\n")
	if code, err := w.run("https://github.com/o/r.git"); code != 0 || err != nil {
		t.Fatalf("%d %v\n%s", code, err, w.all())
	}
	c := w.remote("gh auth login")
	if len(c) != 1 || c[0].stdin != "ghp_secret\n" {
		t.Fatalf("gh login %+v", c)
	}
	for _, call := range w.calls {
		if strings.Contains(strings.Join(call.args, " "), "ghp_secret") {
			t.Errorf("token in argv: %q", call.args)
		}
	}
	has(t, c[0].command(), "gh auth login --hostname 'github.com' --with-token && gh auth setup-git --hostname 'github.com' &&",
		"git config --global --unset-all url.'https://github.com/'.insteadOf || true",
		"git config --global --add url.'https://github.com/'.insteadOf 'git@github.com:'", "git config --global --add url.'https://github.com/'.insteadOf 'ssh://git@github.com/'")
	has(t, w.out.String(), "gh: logged in to github.com (SSH URLs of github.com are cloned over HTTPS)")
	// login before clone
	loginAt, cloneAt := -1, -1
	for i, call := range w.calls {
		if strings.Contains(call.command(), "gh auth login") {
			loginAt = i
		}
		if call.stdin == cloneScript {
			cloneAt = i
		}
	}
	if loginAt < 0 || cloneAt < 0 || loginAt > cloneAt {
		t.Errorf("login at %d, clone at %d", loginAt, cloneAt)
	}
}

func TestADeployKeyIsNotBypassedByTheGithubToken(t *testing.T) {
	key := filepath.Join(t.TempDir(), "deploy")
	os.WriteFile(key, []byte("DEPLOY"), 0o600)
	w := newWorld(t, "VIPER_GH_TOKEN=x\nGIT_DEPLOY_KEY_FILE="+key+"\n")
	if code, err := w.run("git@github.com:o/r.git"); code != 0 || err != nil {
		t.Fatalf("%d %v\n%s", code, err, w.all())
	}
	c := w.remote("gh auth login")
	if len(c) != 1 || !strings.HasSuffix(c[0].command(), "&& true") || strings.Contains(c[0].command(), "--add url.") {
		t.Errorf("rewrite must be off: %v", c)
	}
	has(t, w.out.String(), "gh: logged in to github.com (repository via the deploy key)")
	var scp, chmod, clone sshCall
	for _, call := range w.calls {
		switch {
		case call.args[0] == "scp":
			scp = call
		case strings.HasSuffix(call.command(), "chmod 600 ~/.ssh/deploy_key"):
			chmod = call
		case call.stdin == cloneScript:
			clone = call
		}
	}
	if strings.Join(scp.args, " ") != "scp -F "+w.c.SSHConfig+" -q "+key+" hetzner-claude-web:.ssh/deploy_key" || chmod.args == nil {
		t.Errorf("scp %q chmod %q", scp.args, chmod.args)
	}
	if clone.command() != "bash -s -- 'git@github.com:o/r.git' '/Development/r' '' '1'" {
		t.Errorf("clone command %q", clone.command())
	}
}

func TestNoGithubLoginWithoutAToken(t *testing.T) {
	w := newWorld(t, "")
	w.run("")
	if len(w.remote("gh auth login")) != 0 {
		t.Error("gh login without a token")
	}
	hasNot(t, w.out.String(), "gh: logged in")
}

func TestCloneArgumentsAreQuotedAndTheRepositoryIsRemembered(t *testing.T) {
	w := newWorld(t, "REPO_BRANCH=\"feature/x y\"\n")
	if code, err := w.run("https://git.example.com/o/odd path; rm -rf $HOME.git"); code != 0 || err != nil {
		t.Fatalf("%d %v\n%s", code, err, w.all())
	}
	var clone sshCall
	for _, call := range w.calls {
		if call.stdin == cloneScript {
			clone = call
		}
	}
	want := "bash -s -- 'https://git.example.com/o/odd path; rm -rf $HOME.git' '/Development/odd path; rm -rf $HOME' 'feature/x y' ''"
	if clone.command() != want {
		t.Errorf("command\n%s\nwant\n%s", clone.command(), want)
	}
	if b, _ := os.ReadFile(w.c.RepoState); string(b) != "https://git.example.com/o/odd path; rm -rf $HOME.git\n" {
		t.Errorf("remembered %q", b)
	}
	has(t, w.out.String(), "Ready: ssh hetzner-claude-web (work dir: /Development/odd path; rm -rf $HOME)")
	// given once, remembered for the next run
	w2 := newWorld(t, "")
	os.WriteFile(w2.c.RepoState, []byte("https://user:tok3n@git.example.com/o/r.git\n"), 0o600)
	w2.server = &hetzner.Server{ID: 42, Name: "claude-web", IPv4: "203.0.113.7", Created: w2.now.Add(-time.Hour)}
	w2.run("")
	has(t, w2.out.String(), "Repository (remembered from an earlier start): https://***@git.example.com/o/r.git")
	hasNot(t, w2.all(), "tok3n")
}

func TestClaudeLoginAndGatewayStayOutOfArgv(t *testing.T) {
	w := newWorld(t, "VIPER_CLAUDE_TOKEN=\"sk-ant-secret\\$x'y\"\n")
	w.run("")
	c := w.remote("claude.env")
	if len(c) != 1 || c[0].stdin != "export CLAUDE_CODE_OAUTH_TOKEN='sk-ant-secret$x'\\''y'\n" {
		t.Fatalf("claude login %+v", c)
	}
	has(t, c[0].command(), "umask 077 && mkdir -p ~/.config/viper && cat > ~/.config/viper/claude.env &&", `for f in ~/.bashrc ~/.profile; do`)
	for _, call := range w.calls {
		if strings.Contains(strings.Join(call.args, " "), "sk-ant-secret") {
			t.Errorf("token in argv")
		}
	}
	has(t, w.out.String(), "claude: logged in with VIPER_CLAUDE_TOKEN")

	g := newWorld(t, "VIPER_ANTHROPIC_BASE_URL=https://user:pw@gw.example.com\nVIPER_ANTHROPIC_AUTH_TOKEN=gw-token\nVIPER_CLAUDE_MODEL=glm-4.6\nVIPER_CLAUDE_MAX_CONTEXT_TOKENS=1048576\n")
	g.run("")
	c = g.remote("claude.env")
	want := "export ANTHROPIC_BASE_URL='https://user:pw@gw.example.com'\nexport ANTHROPIC_AUTH_TOKEN='gw-token'\nexport ANTHROPIC_API_KEY=\"\"\nunset CLAUDE_CODE_OAUTH_TOKEN\n" +
		"export ANTHROPIC_MODEL='glm-4.6'\nexport ANTHROPIC_DEFAULT_OPUS_MODEL='glm-4.6'\nexport ANTHROPIC_DEFAULT_SONNET_MODEL='glm-4.6'\n" +
		"export ANTHROPIC_DEFAULT_HAIKU_MODEL='glm-4.6'\nexport CLAUDE_CODE_SUBAGENT_MODEL='glm-4.6'\nexport CLAUDE_CODE_MAX_CONTEXT_TOKENS='1048576'\n"
	if len(c) != 1 || c[0].stdin != want {
		t.Fatalf("gateway env %q", c[0].stdin)
	}
	has(t, g.out.String(), "claude: using model glm-4.6 at https://***@gw.example.com")
	hasNot(t, g.all(), "user:pw", "gw-token")
	// neither: no login
	n := newWorld(t, "")
	n.run("")
	if len(n.remote("claude.env")) != 0 {
		t.Error("login without a token")
	}
}

func TestGitIdentityScriptGetsItsSettings(t *testing.T) {
	w := newWorld(t, "VIPER_GIT_USER_NAME=\"Ada Lovelace\"\nVIPER_GIT_USER_EMAIL=ada@example.com\nVIPER_GH_TOKEN=x\nVIPER_GH_HOST=ghe.example.com\n")
	w.run("")
	var id sshCall
	for _, call := range w.calls {
		if call.stdin == gitIDScript {
			id = call
		}
	}
	if id.command() != "bash -s -- 'Ada Lovelace' 'ada@example.com' 'ghe.example.com' '1'" {
		t.Errorf("command %q", id.command())
	}
	w = newWorld(t, "")
	w.failing["git config --global user.name"] = errors.New("exit 1")
	w.run("")
	has(t, w.err.String(), "WARNING: git user.name/user.email could not be set on the server")
}

func TestSwapIsOptInAndNotFatal(t *testing.T) {
	w := newWorld(t, "")
	w.run("")
	for _, call := range w.calls {
		if call.stdin == swapScript {
			t.Error("swap without VIPER_SWAP")
		}
	}
	w = newWorld(t, "VIPER_SWAP=0.5\n")
	w.failing["swapon"] = errors.New("exit 1")
	code, err := w.run("")
	if code != 0 || err != nil {
		t.Fatalf("%d %v", code, err)
	}
	var swap sshCall
	for _, call := range w.calls {
		if call.stdin == swapScript {
			swap = call
		}
	}
	if swap.command() != "bash -s -- '0.5'" {
		t.Errorf("command %q", swap.command())
	}
	has(t, w.err.String(), "WARNING: swap could not be set up; rerun viper up web")
}

func TestHomebrewIsInstalledSkippedAndFailuresOnlyWarn(t *testing.T) {
	w := newWorld(t, "")
	w.run("")
	c := w.remote("Homebrew/install/HEAD/install.sh")
	if len(c) != 1 || !strings.HasPrefix(c[0].command(), "test -x /home/linuxbrew/.linuxbrew/bin/brew || {") {
		t.Fatalf("install %+v", c)
	}
	has(t, w.out.String(), "Homebrew: installing (skipped if present) ...")
	w = newWorld(t, "VIPER_HOMEBREW=0\n")
	w.run("")
	if len(w.remote("Homebrew/install")) != 0 {
		t.Error("installed although off")
	}
	w = newWorld(t, "")
	w.failing["Homebrew/install"] = errors.New("exit 1")
	if code, _ := w.run(""); code != 0 {
		t.Errorf("a failed install is not fatal: %d", code)
	}
	has(t, w.err.String(), "WARNING: Homebrew installation failed (log on the server: ~/.config/viper/homebrew-install.log); rerun viper up web")
}

func TestBrewTapsTrustAndPackagesGoToTheScriptInOrder(t *testing.T) {
	w := newWorld(t, "VIPER_BREW_TAPS=\"hashicorp/tap o/t=https://git.example.com/o/t.git\"\nVIPER_BREW_TRUST=hashicorp/tap/terraform\nVIPER_BREW_PACKAGES=\"ripgrep fd\"\nVIPER_BREWFILE=1\n")
	w.run("https://git.example.com/o/r.git")
	var brew sshCall
	for _, call := range w.calls {
		if call.stdin == brewPkgScript {
			brew = call
		}
	}
	want := "bash -s -- '/home/linuxbrew/.linuxbrew/bin/brew' '/Development/r/Brewfile' '2' '1' 'hashicorp/tap' 'o/t=https://git.example.com/o/t.git' 'hashicorp/tap/terraform' 'ripgrep' 'fd'"
	if brew.command() != want {
		t.Errorf("command\n%s\nwant\n%s", brew.command(), want)
	}
	// nothing asked for: no script
	n := newWorld(t, "")
	n.run("")
	for _, call := range n.calls {
		if call.stdin == brewPkgScript {
			t.Error("brew script without settings")
		}
	}
	// a failing script only warns
	f := newWorld(t, "VIPER_BREW_PACKAGES=ripgrep\n")
	f.failing["brew install"] = errors.New("x")
	f.failing["Homebrew missing"] = errors.New("x")
	f.run("")
	for _, call := range f.calls {
		if call.stdin == brewPkgScript {
			return
		}
	}
	t.Error("brew script not run")
}

func TestCodeServerIsOptInWithThePasswordOnStdin(t *testing.T) {
	w := newWorld(t, "")
	w.run("")
	if len(w.remote("code-server")) != 0 {
		t.Error("code-server without VIPER_CODE_SERVER")
	}
	w = newWorld(t, "VIPER_CODE_SERVER=1\nVIPER_CODE_SERVER_PASSWORD='p\"ss $word!<x'\n")
	if code, err := w.run(""); code != 0 || err != nil {
		t.Fatalf("%d %v", code, err)
	}
	c := w.remote("config.yaml")
	if len(c) != 1 || !strings.HasPrefix(c[0].stdin, "bind-addr: 127.0.0.1:8080\nauth: password\ncert: false\npassword: \"p\\\"ss $word!") {
		t.Fatalf("config %+v", c)
	}
	for _, call := range w.calls {
		if strings.Contains(strings.Join(call.args, " "), "word!") {
			t.Error("password in argv")
		}
	}
	has(t, w.out.String(), "code-server: running on the server at 127.0.0.1:8080; tunnel: ssh -L 8080:127.0.0.1:8080 hetzner-claude-web, then http://127.0.0.1:8080")
	f := newWorld(t, "VIPER_CODE_SERVER=1\nVIPER_CODE_SERVER_PASSWORD=long-enough-pw-1\n")
	f.failing["code-server.dev/install.sh"] = errors.New("exit 1")
	if code, _ := f.run(""); code != 0 {
		t.Errorf("not fatal: %d", code)
	}
	has(t, f.err.String(), "WARNING: code-server installation failed (log on the server: ~/.config/viper/code-server-install.log); rerun viper up web")
	if len(f.remote("config.yaml")) != 0 {
		t.Error("configured although the install failed")
	}
}

func TestTheShellSetupAndInventoryRunLast(t *testing.T) {
	w := newWorld(t, "")
	w.run("https://git.example.com/o/r.git")
	var cd, inv int = -1, -1
	for i, call := range w.calls {
		if call.stdin == cdLineScript {
			cd = i
			if call.command() != "bash -s -- '/Development/r' '/Development' '/home/linuxbrew/.linuxbrew/bin/brew' '42'" {
				t.Errorf("cdline %q", call.command())
			}
		}
		if call.stdin == inventoryScript {
			inv = i
			cmd := call.command()
			has(t, cmd, "'/home/linuxbrew/.linuxbrew/bin/brew' 'gnupg' 'git' 'gh'", "'openssh-server'", "'docker-ce' 'docker-ce-cli' 'containerd.io' 'docker-buildx-plugin' 'docker-compose-plugin'")
		}
	}
	if cd < 0 || inv < 0 || cd > inv || inv != len(w.calls)-1 {
		t.Errorf("cdline at %d, inventory at %d of %d", cd, inv, len(w.calls))
	}
	if b, _ := os.ReadFile(w.c.ReadyState); string(b) != "42\n" {
		t.Errorf("ready state %q", b)
	}
	// an inventory that cannot be made does not fail the run
	f := newWorld(t, "")
	f.failing["os: "] = errors.New("exit 1")
	f.failing["dpkg-query"] = errors.New("exit 1")
	if code, _ := f.run(""); code != 0 {
		t.Errorf("%d", code)
	}
	has(t, f.out.String(), "(inventory not available)")
}

func TestDnsFailureStopsBeforeWaiting(t *testing.T) {
	w := newWorld(t, "")
	w.dnsErr = errors.New("Existing DNS record is not managed by this setup; refusing overwrite")
	code, err := w.run("")
	if code != 1 || !errors.Is(err, ErrShown) || w.probeN != 0 {
		t.Fatalf("%d %v probes %d", code, err, w.probeN)
	}
	has(t, w.err.String(), "Existing DNS record is not managed by this setup; refusing overwrite")
	if len(w.created) != 1 {
		t.Error("the server was created before the DNS step, as in the script")
	}
}

func TestAnInterruptionEndsTheRunWithoutMisleadingWarnings(t *testing.T) {
	w := newWorld(t, "VIPER_SWAP=1\nVIPER_CODE_SERVER=1\nVIPER_CODE_SERVER_PASSWORD=long-enough-pw-1\n")
	ctx, cancel := context.WithCancel(context.Background())
	d := w.deps()
	stream := d.Stream
	d.Stream = func(c context.Context, stdin string, o, e io.Writer, name string, args ...string) error {
		if strings.Contains(args[len(args)-1], "sudo install -d") { // the workspace step: Ctrl-C arrives here
			cancel()
			return context.Canceled
		}
		return stream(c, stdin, o, e, name, args...)
	}
	code, err := Run(ctx, w.c, "", d, w.lg, &w.out, &w.err)
	w.lg.Close(err, code)
	if code != 1 || !errors.Is(err, ErrShown) {
		t.Fatalf("%d %v", code, err)
	}
	hasNot(t, w.err.String(), "WARNING")
	if len(w.remote("claude.env"))+len(w.remote("Homebrew"))+len(w.remote("code-server")) != 0 {
		t.Error("steps went on after the interruption")
	}
}

func TestAnInterruptedClaudeInstallDoesNotWaitOrRetry(t *testing.T) {
	w := newWorld(t, "")
	w.probes = []func() (string, string, error){func() (string, string, error) { return "cloud-init=0\ndocker=ok\nready=\n", "", nil }}
	ctx, cancel := context.WithCancel(context.Background())
	d := w.deps()
	d.Stream = func(c context.Context, stdin string, o, e io.Writer, name string, args ...string) error {
		cancel()
		return context.Canceled
	}
	code, err := Run(ctx, w.c, "", d, w.lg, &w.out, &w.err)
	w.lg.Close(err, code)
	if code != 1 || !errors.Is(err, ErrShown) {
		t.Fatalf("%d %v", code, err)
	}
	for _, s := range w.sleeps {
		if s >= 30*time.Second {
			t.Errorf("slept %v after the interruption", s)
		}
	}
	hasNot(t, w.err.String(), "retrying in")
}
