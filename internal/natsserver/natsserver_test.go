package natsserver_test

import (
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/natsserver"
)

const keyA = "UABCDEFGHIJKLMNOPQRSTUVWXYZ234567ABCDEFGHIJKLMNOPQRSTUVW"

func TestReleaseForPicksTheArchitectureAndPinsTheChecksum(t *testing.T) {
	for in, arch := range map[string]string{"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"} {
		r, err := natsserver.ReleaseFor(in)
		if err != nil || r.Arch != arch || len(r.SHA256) != 64 || !strings.HasSuffix(r.URL, "linux-"+arch+".tar.gz") || !strings.Contains(r.URL, natsserver.Version) {
			t.Errorf("%s: %+v %v", in, r, err)
		}
	}
	a, _ := natsserver.ReleaseFor("x86_64")
	b, _ := natsserver.ReleaseFor("aarch64")
	if a.SHA256 == b.SHA256 {
		t.Error("two architectures, two tarballs, two sums")
	}
	if _, err := natsserver.ReleaseFor("riscv64"); err == nil {
		t.Error("no release for riscv64")
	}
}

func TestRenderListensOnThePrivateIPOnlyAndGivesEachMachineItsSubjects(t *testing.T) {
	out, err := natsserver.Config{Group: "dev", Host: "10.130.0.2", Port: 4222, Clients: []natsserver.Client{
		{Machine: "beta", PublicKey: keyA}, {Machine: "alpha", PublicKey: keyA},
	}}.Render()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`host: "10.130.0.2"`, "port: 4222", `server_name: "viper-dev"`, "cert_file: \"" + natsserver.CertFile + "\"", `min_version: "1.2"`,
		`publish: { allow: ["viper.dev.alpha.>", "viper.dev.*.req.>"] }`,
		`subscribe: { allow: ["viper.dev.>", "_INBOX.beta.>"] }`,
		"allow_responses: true", "nkey: " + keyA,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Index(out, "# alpha") > strings.Index(out, "# beta") {
		t.Error("clients are sorted, so the file only changes when the group does")
	}
	for _, bad := range []string{"0.0.0.0", "http_port", "jetstream", "8222", "6222", "cluster", `allow: [">"]`} {
		if strings.Contains(out, bad) {
			t.Errorf("must not contain %q", bad)
		}
	}
}

func TestRenderRefusesWhatWouldWidenTheServer(t *testing.T) {
	ok := natsserver.Config{Group: "dev", Host: "10.130.0.2", Port: 4222, Clients: []natsserver.Client{{Machine: "alpha", PublicKey: keyA}}}
	if _, err := ok.Render(); err != nil {
		t.Fatal(err)
	}
	with := func(f func(c *natsserver.Config)) error {
		c := ok
		c.Clients = append([]natsserver.Client(nil), ok.Clients...)
		f(&c)
		_, err := c.Render()
		return err
	}
	for name, f := range map[string]func(c *natsserver.Config){
		"all interfaces":  func(c *natsserver.Config) { c.Host = "0.0.0.0" },
		"public address":  func(c *natsserver.Config) { c.Host = "203.0.113.7" },
		"a name":          func(c *natsserver.Config) { c.Host = "localhost" },
		"ipv6":            func(c *natsserver.Config) { c.Host = "fd00::1" },
		"low port":        func(c *natsserver.Config) { c.Port = 80 },
		"group with dots": func(c *natsserver.Config) { c.Group = "a.b" },
		"machine wild":    func(c *natsserver.Config) { c.Clients[0].Machine = "a*" },
		"machine gt":      func(c *natsserver.Config) { c.Clients[0].Machine = "a>" },
		"seed not public": func(c *natsserver.Config) { c.Clients[0].PublicKey = "S" + keyA[1:] },
		"short key":       func(c *natsserver.Config) { c.Clients[0].PublicKey = "UABC" },
		"no clients":      func(c *natsserver.Config) { c.Clients = nil },
		"twice":           func(c *natsserver.Config) { c.Clients = append(c.Clients, c.Clients[0]) },
	} {
		if err := with(f); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
}

func TestPermissionsStayInTheGroup(t *testing.T) {
	pub, sub := natsserver.Permissions("dev", "beta")
	all := strings.Join(append(append([]string{}, pub...), sub...), " ")
	if strings.Contains(all, " >") || strings.HasPrefix(all, ">") || strings.Contains(all, "$SYS") {
		t.Errorf("too wide: %s", all)
	}
	for _, s := range append(append([]string{}, pub...), sub...) {
		if !strings.HasPrefix(s, "viper.dev.") && !strings.HasPrefix(s, "_INBOX.beta.") {
			t.Errorf("%s leaves the group", s)
		}
	}
}

func TestUnitFileStartsAfterTheNetworkAndRetries(t *testing.T) {
	u := natsserver.UnitFile()
	for _, want := range []string{"After=network-online.target", "Restart=on-failure", "User=builder", "NoNewPrivileges=yes", "ProtectSystem=strict", "ExecStart=" + natsserver.Binary + " -c " + natsserver.ConfigFile, "ExecReload=/bin/kill -HUP $MAINPID"} {
		if !strings.Contains(u, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestNewIdentityIsAUserKeyPairThatTheConfigAccepts(t *testing.T) {
	a, err := natsserver.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := natsserver.NewIdentity()
	if !strings.HasPrefix(a.Seed, "SU") || !strings.HasPrefix(a.PublicKey, "U") || a.PublicKey == b.PublicKey || a.Seed == b.Seed {
		t.Fatalf("%+v %+v", a, b)
	}
	if _, err := (natsserver.Config{Group: "dev", Host: "10.130.0.2", Port: 4222, Clients: []natsserver.Client{{Machine: "alpha", PublicKey: a.PublicKey}}}).Render(); err != nil {
		t.Errorf("a real public key must pass the check: %v", err)
	}
}

func TestClientsSurviveARenderAndAreReadBack(t *testing.T) {
	a, _ := natsserver.NewIdentity()
	b, _ := natsserver.NewIdentity()
	cfg := natsserver.Config{Group: "dev", Host: "10.130.0.2", Port: 4222, Clients: []natsserver.Client{{Machine: "beta", PublicKey: b.PublicKey}, {Machine: "alpha", PublicKey: a.PublicKey}}}
	out, err := cfg.Render()
	if err != nil {
		t.Fatal(err)
	}
	got := natsserver.ParseClients(out)
	if len(got) != 2 || got[0] != (natsserver.Client{Machine: "alpha", PublicKey: a.PublicKey}) || got[1].Machine != "beta" {
		t.Fatalf("%+v", got)
	}
	// the second render of what was read back is the same file
	cfg.Clients = got
	again, _ := cfg.Render()
	if again != out {
		t.Error("a render of the clients read back must give the same file")
	}
	if natsserver.ParseClients("") != nil || len(natsserver.ParseClients("# viper-client onlyname\n# other\n")) != 0 {
		t.Error("nothing to read")
	}
}

func TestWithAndWithoutClient(t *testing.T) {
	c := []natsserver.Client{{Machine: "a", PublicKey: "k1"}, {Machine: "b", PublicKey: "k2"}}
	got := natsserver.WithClient(c, natsserver.Client{Machine: "a", PublicKey: "k3"})
	if len(got) != 2 || got[1] != (natsserver.Client{Machine: "a", PublicKey: "k3"}) {
		t.Errorf("replace: %+v", got)
	}
	if got := natsserver.WithClient(c, natsserver.Client{Machine: "c", PublicKey: "k4"}); len(got) != 3 {
		t.Errorf("add: %+v", got)
	}
	if got := natsserver.WithoutClient(c, "a"); len(got) != 1 || got[0].Machine != "b" {
		t.Errorf("remove: %+v", got)
	}
	if len(c) != 2 || c[0].PublicKey != "k1" {
		t.Error("the input is not changed")
	}
}

func TestClientEnvNamesTheServerAndTheFiles(t *testing.T) {
	e := natsserver.ClientEnv("dev", "beta", "10.130.0.2", 4222)
	for _, want := range []string{"NATS_URL=tls://10.130.0.2:4222", "NATS_CA_FILE=" + natsserver.CertFile, "NATS_NKEY_SEED_FILE=" + natsserver.Dir + "/client.nk", "NATS_INBOX_PREFIX=_INBOX.beta", "VIPER_NATS_SUBJECT_PREFIX=viper.dev.beta"} {
		if !strings.Contains(e, want) {
			t.Errorf("missing %q in\n%s", want, e)
		}
	}
}

func TestCLIReleaseForPinsTheTool(t *testing.T) {
	r, err := natsserver.CLIReleaseFor("aarch64")
	if err != nil || r.Arch != "arm64" || len(r.SHA256) != 64 || !strings.HasSuffix(r.URL, "nats-0.5.1-linux-arm64.zip") {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := natsserver.CLIReleaseFor("mips"); err == nil {
		t.Error("no release for mips")
	}
}
