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
		"nkey: " + keyA,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Index(out, "# alpha") > strings.Index(out, "# beta") {
		t.Error("clients are sorted, so the file only changes when the group does")
	}
	for _, bad := range []string{"0.0.0.0", "http_port", "jetstream", "8222", "6222", "cluster", `allow: [">"]`, "allow_responses"} {
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
	pub, sub := natsserver.Permissions("dev", "beta", false)
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
	u := natsserver.UnitFile(false)
	for _, want := range []string{"After=network-online.target", "Restart=on-failure", "User=builder", "NoNewPrivileges=yes", "ProtectSystem=strict", "ExecStart=" + natsserver.Binary + " -c " + natsserver.ConfigFile, "ExecReload=/bin/kill -HUP $MAINPID"} {
		if !strings.Contains(u, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestTheUnitHasAStateDirectoryOnlyForAnObjectStore(t *testing.T) {
	if strings.Contains(natsserver.UnitFile(false), "StateDirectory") {
		t.Error("no object store, no state directory")
	}
	u := natsserver.UnitFile(true)
	if !strings.Contains(u, "StateDirectory=viper-nats\n") || strings.Index(u, "ReadOnlyPaths=") > strings.Index(u, "StateDirectory=") {
		t.Errorf("the state directory is in the [Service] part:\n%s", u)
	}
}

func objectsConfig(o *natsserver.Objects) natsserver.Config {
	return natsserver.Config{Group: "dev", Host: "10.130.0.2", Port: 4222, Objects: o, Clients: []natsserver.Client{{Machine: "alpha", PublicKey: keyA}}}
}

func TestRenderTurnsJetStreamOnOnlyForAnObjectStoreAndKeepsTheLimits(t *testing.T) {
	o := &natsserver.Objects{StoreDir: natsserver.ObjectStoreDirVM, MaxBytes: 256 << 20, FileMax: 8 << 20, TTLDays: 7}
	out, err := objectsConfig(o).Render()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"jetstream {", `store_dir: "/var/lib/viper-nats"`, "max_file_store: 268435456", "# viper-objects store=/var/lib/viper-nats max=268435456 file=8388608 ttl=7"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// the endpoint read back from the file has the store, so that a change of the machines does not turn it off
	end, ok := natsserver.ParseEndpoint(out)
	if !ok || end.Objects == nil || *end.Objects != *o {
		t.Fatalf("%+v %v", end, ok)
	}
	again, err := objectsConfig(end.Objects).Render()
	if err != nil || again != out {
		t.Errorf("the file changes when it is rendered again from what it says: %v", err)
	}
	if off, _ := objectsConfig(nil).Render(); strings.Contains(off, "jetstream") || strings.Contains(off, "viper-objects") {
		t.Errorf("no object store, no JetStream:\n%s", off)
	}
	if end, _ := natsserver.ParseEndpoint(strings.Replace(out, "# viper-objects", "# viper-objectz", 1)); end.Objects != nil {
		t.Error("a line that is not ours is not read")
	}
}

func TestRenderRefusesAnObjectStoreOutsideTheRange(t *testing.T) {
	good := natsserver.Objects{StoreDir: natsserver.Dir + "/js", MaxBytes: 64 << 20, FileMax: 8 << 20, TTLDays: 7}
	if _, err := objectsConfig(&good).Render(); err != nil {
		t.Fatal(err)
	}
	for name, mod := range map[string]func(*natsserver.Objects){
		"another directory":     func(o *natsserver.Objects) { o.StoreDir = "/tmp/js" },
		"a path with a quote":   func(o *natsserver.Objects) { o.StoreDir = natsserver.Dir + `/js"; x` },
		"too small":             func(o *natsserver.Objects) { o.MaxBytes = 1 << 20 },
		"too large":             func(o *natsserver.Objects) { o.MaxBytes = 1 << 40 },
		"a file over the store": func(o *natsserver.Objects) { o.FileMax = o.MaxBytes + 1 },
		"no file":               func(o *natsserver.Objects) { o.FileMax = 0 },
		"a huge file":           func(o *natsserver.Objects) { o.FileMax = 65 << 20 },
		"no time":               func(o *natsserver.Objects) { o.TTLDays = 0 },
		"too long":              func(o *natsserver.Objects) { o.TTLDays = 366 },
	} {
		o := good
		mod(&o)
		if _, err := objectsConfig(&o).Render(); err == nil {
			t.Errorf("%s was accepted", name)
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

func TestParseEndpointReadsWhatRenderWrote(t *testing.T) {
	id, _ := natsserver.NewIdentity()
	out, err := natsserver.Config{Group: "dev", Host: "10.130.0.2", Port: 4333, Clients: []natsserver.Client{{Machine: "alpha", PublicKey: id.PublicKey}}}.Render()
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := natsserver.ParseEndpoint(out); !ok || e != (natsserver.Endpoint{Host: "10.130.0.2", Port: 4333}) {
		t.Errorf("%+v %v", e, ok)
	}
	if _, ok := natsserver.ParseEndpoint("# nothing"); ok {
		t.Error("no endpoint in there")
	}
}

func TestAServerOnADockerNetworkIsReachedByTheAliasOfOneLabel(t *testing.T) {
	cfg := natsserver.Config{Group: "dev", Host: "viper-nats-dev", Listen: natsserver.ListenPodIP, Port: 4222,
		Clients: []natsserver.Client{{Machine: "alpha", PublicKey: keyA}}}
	out, err := cfg.Render()
	if err != nil {
		t.Fatal(err)
	}
	if end, ok := natsserver.ParseEndpoint(out); !ok || end.Host != "viper-nats-dev" || end.Listen != natsserver.ListenPodIP {
		t.Errorf("%+v %v", end, ok)
	}
}

func TestAServerInAPodListensOnThePodAddressAndIsReachedByTheServiceName(t *testing.T) {
	cfg := natsserver.Config{Group: "dev", Host: "viper-nats-dev.viper.svc", Listen: natsserver.ListenPodIP, Port: 4222,
		Clients: []natsserver.Client{{Machine: "alpha", PublicKey: keyA}}}
	out, err := cfg.Render()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "\nhost: $POD_IP\n") || strings.Contains(out, `host: "`) {
		t.Errorf("the address of the pod comes from the environment, unquoted:\n%s", out)
	}
	end, ok := natsserver.ParseEndpoint(out)
	if !ok || end.Host != "viper-nats-dev.viper.svc" || end.Listen != natsserver.ListenPodIP || end.Port != 4222 {
		t.Errorf("%+v %v", end, ok)
	}
	// the clients are read back as before, the line with the name is not one of them
	if got := natsserver.ParseClients(out); len(got) != 1 || got[0].Machine != "alpha" {
		t.Errorf("%+v", got)
	}
	// a VM keeps its quoted private address and has no Listen
	vm, _ := natsserver.Config{Group: "dev", Host: "10.130.0.2", Port: 4222, Clients: cfg.Clients}.Render()
	if e, _ := natsserver.ParseEndpoint(vm); e.Host != "10.130.0.2" || e.Listen != "" {
		t.Errorf("%+v", e)
	}
	for name, bad := range map[string]natsserver.Config{
		"another listen address": {Group: "dev", Host: "viper-nats-dev.viper.svc", Listen: "0.0.0.0", Port: 4222, Clients: cfg.Clients},
		"a host that is no name": {Group: "dev", Host: "10.0.0.1", Listen: natsserver.ListenPodIP, Port: 4222, Clients: cfg.Clients},
		"a name with a quote":    {Group: "dev", Host: "a.b\"c", Listen: natsserver.ListenPodIP, Port: 4222, Clients: cfg.Clients},
		"a name with a newline":  {Group: "dev", Host: "a.b\nc.d", Listen: natsserver.ListenPodIP, Port: 4222, Clients: cfg.Clients},
	} {
		if _, err := bad.Render(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
