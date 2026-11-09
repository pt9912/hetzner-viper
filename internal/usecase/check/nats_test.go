package check_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/servertest"
	"github.com/pt9912/hetzner-viper/internal/usecase/check"
)

// natsFake gives the server fake of the check tests the NATS capability of the fake cloud.
type natsFake struct {
	serverFake
	nc *servertest.Cloud
}

func (n natsFake) ListGroupMachines(ctx context.Context, g string) ([]model.GroupMember, error) {
	return n.nc.ListGroupMachines(ctx, g)
}
func (n natsFake) UpdateNATSMetadata(ctx context.Context, id string, m model.NATSMetadata) error {
	return n.nc.UpdateNATSMetadata(ctx, id, m)
}
func (n natsFake) NATSMetadataOf(ctx context.Context, id string) (*model.NATSMetadata, error) {
	return n.nc.NATSMetadataOf(ctx, id)
}
func (n natsFake) NetworkZone(ctx context.Context, l string) (string, error) {
	return n.nc.NetworkZone(ctx, l)
}
func (n natsFake) EnsurePrivateNetwork(ctx context.Context, g, l string) (model.PrivateNetwork, bool, error) {
	return n.nc.EnsurePrivateNetwork(ctx, g, l)
}
func (n natsFake) FindPrivateNetwork(ctx context.Context, g string) (*model.PrivateNetwork, error) {
	return n.nc.FindPrivateNetwork(ctx, g)
}
func (n natsFake) AttachPrivateNetwork(ctx context.Context, s, nw string) (string, error) {
	return n.nc.AttachPrivateNetwork(ctx, s, nw)
}
func (n natsFake) DeletePrivateNetwork(ctx context.Context, nw string) error {
	return n.nc.DeletePrivateNetwork(ctx, nw)
}

const healthyClient = `env=ok
url=tls://10.115.0.2:4222
cert_days=25
cert_ip=10.115.0.2
login=ok
`

const healthyServer = `env=ok
url=tls://10.115.0.2:4222
cert_days=25
cert_ip=10.115.0.2
login=ok
server=yes
unit=active
listen=10.115.0.2:4222
registered=alpha,web
`

type natsFixture struct {
	*fixture
	c       *model.Machine
	nc      *servertest.Cloud
	status  string
	dialErr error // nil: the port answers
	srv     *model.Server
}

// group is dev with the server alpha (ready) and the machine web (client, or the server itself).
func newNATSFixture(t *testing.T, settings string, webRole model.NATSRole, status string) *natsFixture {
	t.Helper()
	f, c := newFixture(t, settings)
	nf := &natsFixture{fixture: f, c: c, nc: servertest.NewCloud(time.Now()), status: status, dialErr: errors.New("refused")}
	ctx := context.Background()
	net, _, err := nf.nc.EnsurePrivateNetwork(ctx, "dev", "fsn1")
	if err != nil {
		t.Fatal(err)
	}
	mk := func(name string, r model.NATSRole) *model.Server {
		s, err := nf.nc.CreateServer(ctx, model.ServerSpec{Name: name, Network: net.ID,
			Labels: model.NATSLabels(model.NATSMetadata{Group: "dev", Role: r, State: model.NATSReady})})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if webRole == model.NATSServer {
		nf.srv = mk("claude-web", model.NATSServer)
		mk("claude-beta", model.NATSClient)
	} else {
		mk("claude-alpha", model.NATSServer)
		nf.srv = mk("claude-web", model.NATSClient)
	}
	nf.srv.IPv4 = "203.0.113.7"
	f.server = nf.srv
	return nf
}

func (n *natsFixture) run() (string, int) {
	n.t.Helper()
	d := n.deps()
	d.Cloud = natsFake{serverFake: serverFake{f: n.fixture}, nc: n.nc}
	inner := d.Run
	d.Run = func(ctx context.Context, stdin, name string, args ...string) (string, string, error) {
		if name == "ssh" && strings.Contains(stdin, "nats_status") {
			if n.status == "SSHFAIL" {
				return "", "", errors.New("exit 255")
			}
			return n.status, "", nil
		}
		return inner(ctx, stdin, name, args...)
	}
	d.Dial = func(context.Context, string, string) (net.Conn, error) {
		if n.dialErr != nil {
			return nil, n.dialErr
		}
		c, _ := net.Pipe()
		return c, nil
	}
	var out, errb strings.Builder
	problems, err := check.Run(context.Background(), n.c, d, &out, &errb)
	if err != nil {
		n.t.Fatalf("%v\n%s", err, out.String())
	}
	return out.String(), problems
}

func natsSection(out string) string {
	_, rest, ok := strings.Cut(out, "== NATS")
	if !ok {
		return ""
	}
	return "== NATS" + rest
}

func TestAHealthyClientPassesAllNATSChecks(t *testing.T) {
	n := newNATSFixture(t, "", model.NATSClient, healthyClient)
	out, problems := n.run()
	sec := natsSection(out)
	if problems != 0 || strings.Contains(sec, "FAIL") || strings.Contains(sec, "WARN") {
		t.Fatalf("%d problems:\n%s", problems, sec)
	}
	for _, want := range []string{"== NATS (group dev, client)", "exactly one server in the group: claude-alpha", "server state of claude-alpha: ready",
		"private network viper-nats-dev", "private address 10.115.0.", "server certificate valid for 25 more days",
		"logged in to tls://10.115.0.2:4222 over TLS with the NKey", "203.0.113.7:4222 is not reachable from the internet"} {
		if !strings.Contains(sec, want) {
			t.Errorf("missing %q in\n%s", want, sec)
		}
	}
}

func TestAHealthyServerPassesAllNATSChecks(t *testing.T) {
	st := strings.Replace(healthyServer, "alpha,web", "beta,web", 1)
	n := newNATSFixture(t, "", model.NATSServer, st)
	out, problems := n.run()
	sec := natsSection(out)
	if problems != 0 || strings.Contains(sec, "FAIL") || strings.Contains(sec, "WARN") {
		t.Fatalf("%d problems:\n%s", problems, sec)
	}
	for _, want := range []string{"service viper-nats is active", "the server listens only on 10.115.0.2:4222", "2 machine(s) registered at the server"} {
		if !strings.Contains(sec, want) {
			t.Errorf("missing %q in\n%s", want, sec)
		}
	}
}

func TestTheServerCheckFindsOrphanedAndMissingRegistrations(t *testing.T) {
	st := strings.Replace(healthyServer, "alpha,web", "gone,web", 1) // gone: no machine; beta (ready) is not registered
	n := newNATSFixture(t, "", model.NATSServer, st)
	out, problems := n.run()
	sec := natsSection(out)
	if problems != 1 {
		t.Errorf("one problem (beta missing; the orphan is a warning): %d\n%s", problems, sec)
	}
	for _, want := range []string{"WARN  registered at the server, but no machine of the group any more: gone", "FAIL  ready machines that are not registered at the server: beta"} {
		if !strings.Contains(sec, want) {
			t.Errorf("missing %q in\n%s", want, sec)
		}
	}
}

func TestNATSProblemsAreReportedWithTheirCause(t *testing.T) {
	cases := map[string]struct {
		status string
		dial   error
		want   string
	}{
		"login failed":   {strings.Replace(healthyClient, "login=ok", "login=failed\nlogin_err=nats: authorization violation", 1), errors.New("x"), "FAIL  the login to tls://10.115.0.2:4222 failed: nats: authorization violation"},
		"cert expired":   {strings.Replace(healthyClient, "cert_days=25", "cert_days=-2", 1), errors.New("x"), "FAIL  the server certificate has expired"},
		"cert soon":      {strings.Replace(healthyClient, "cert_days=25", "cert_days=3", 1), errors.New("x"), "WARN  the server certificate expires in 3 days"},
		"cert other ip":  {strings.Replace(healthyClient, "cert_ip=10.115.0.2", "cert_ip=10.115.0.9", 1), errors.New("x"), "FAIL  the certificate is for 10.115.0.9, but the server is at 10.115.0.2 now"},
		"no env":         {"env=missing\n", errors.New("x"), "FAIL  /home/builder/.viper-nats/connection.env is missing"},
		"port is public": {healthyClient, nil, "FAIL  203.0.113.7:4222 answers from the internet"},
		"cert missing":   {strings.Replace(healthyClient, "cert_days=25", "cert_days=missing", 1), errors.New("x"), "FAIL  the server certificate is missing"},
		"ssh fails":      {"SSHFAIL", errors.New("x"), "FAIL  SSH to"},
	}
	for name, tc := range cases {
		n := newNATSFixture(t, "", model.NATSClient, tc.status)
		n.dialErr = tc.dial
		out, problems := n.run()
		if !strings.Contains(natsSection(out), tc.want) {
			t.Errorf("%s: missing %q in\n%s", name, tc.want, natsSection(out))
		}
		if strings.HasPrefix(tc.want, "FAIL") && problems == 0 {
			t.Errorf("%s: a FAIL must count as a problem", name)
		}
	}
}

func TestTheServerThatListensOnAllAddressesFails(t *testing.T) {
	n := newNATSFixture(t, "", model.NATSServer, strings.Replace(strings.Replace(healthyServer, "listen=10.115.0.2:4222", "listen=0.0.0.0:4222", 1), "alpha,web", "beta,web", 1))
	out, problems := n.run()
	if problems == 0 || !strings.Contains(natsSection(out), "FAIL  the server listens on all addresses (0.0.0.0:4222)") {
		t.Errorf("%d\n%s", problems, natsSection(out))
	}
}

func TestGroupProblemsAreFoundInTheCloudData(t *testing.T) {
	ctx := context.Background()
	n := newNATSFixture(t, "", model.NATSClient, healthyClient)
	// a second server and a failed one
	for _, name := range []string{"claude-gamma"} {
		if _, err := n.nc.CreateServer(ctx, model.ServerSpec{Name: name, Labels: model.NATSLabels(model.NATSMetadata{Group: "dev", Role: model.NATSServer, State: model.NATSFailed})}); err != nil {
			t.Fatal(err)
		}
	}
	out, problems := n.run()
	sec := natsSection(out)
	if problems == 0 || !strings.Contains(sec, "FAIL  the group dev has 2 servers, there must be exactly one") || !strings.Contains(sec, "FAIL  server state of claude-gamma: failed") {
		t.Errorf("%d\n%s", problems, sec)
	}

	n = newNATSFixture(t, "", model.NATSClient, healthyClient)
	for _, s := range n.nc.Servers {
		if s.Name == "claude-alpha" {
			_ = n.nc.DeleteServer(ctx, s.ID)
			break
		}
	}
	out, _ = n.run()
	if !strings.Contains(natsSection(out), "FAIL  the group dev has no server") {
		t.Errorf("no server:\n%s", natsSection(out))
	}
}

func TestAMachineInAnotherGroupThanConfiguredFails(t *testing.T) {
	n := newNATSFixture(t, "NATS_ENABLED=true\nNATS_GROUP=prod\n", model.NATSClient, healthyClient)
	out, problems := n.run()
	if problems == 0 || !strings.Contains(natsSection(out), "NATS_GROUP is prod, but claude-web belongs to the group dev") {
		t.Errorf("%d\n%s", problems, natsSection(out))
	}
}

func TestMachinesWithoutNATSGetNoSection(t *testing.T) {
	n := newNATSFixture(t, "", model.NATSClient, healthyClient)
	plain, _ := n.nc.CreateServer(context.Background(), model.ServerSpec{Name: "claude-plain"})
	plain.IPv4 = "203.0.113.7"
	n.server = plain
	out, problems := n.run()
	if problems != 0 || strings.Contains(out, "NATS") {
		t.Errorf("%d problems; no word about NATS expected:\n%s", problems, out)
	}
	// with NATS switched on for it, it says that the machine is in no group yet
	n2 := newNATSFixture(t, "NATS_ENABLED=true\nNATS_GROUP=dev\n", model.NATSClient, healthyClient)
	plain2, _ := n2.nc.CreateServer(context.Background(), model.ServerSpec{Name: "claude-plain"})
	plain2.IPv4 = "203.0.113.7"
	n2.server = plain2
	out, problems = n2.run()
	if problems != 0 || !strings.Contains(out, "WARN  NATS_ENABLED is on, but claude-web is in no NATS group yet") {
		t.Errorf("%d\n%s", problems, out)
	}
}

func TestTheServerCheckWarnsAboutAnOldConfigurationWithAllowResponses(t *testing.T) {
	st := strings.Replace(healthyServer, "alpha,web", "beta,web", 1) + "allow_responses=3\n"
	n := newNATSFixture(t, "", model.NATSServer, st)
	out, problems := n.run()
	if !strings.Contains(natsSection(out), "WARN  server.conf still has allow_responses") {
		t.Errorf("%d\n%s", problems, natsSection(out))
	}
	if problems != 0 {
		t.Errorf("a warning is no problem: %d", problems)
	}
	// a configuration without it (or a status from a machine that is no server) says nothing
	ok := newNATSFixture(t, "", model.NATSServer, strings.Replace(healthyServer, "alpha,web", "beta,web", 1)+"allow_responses=0\n")
	out, _ = ok.run()
	if strings.Contains(natsSection(out), "allow_responses") {
		t.Errorf("clean configuration:\n%s", natsSection(out))
	}
}

const mcpOn = "NATS_ENABLED=true\nNATS_GROUP=dev\nNATS_MCP=true\nNATS_MCP_PUSH_FROM=alpha\nNATS_MCP_AUTOALLOW=read\n"

func TestAHealthyMCPSetupPassesTheChecks(t *testing.T) {
	st := healthyClient + "mcp_bin=ok\nmcp_version=viper-nats-mcp v1\nmcp_claude=/home/builder/.viper-nats/viper-nats-mcp\nmcp_allowed=peers,inbox\nmcp_push=alpha\n"
	n := newNATSFixture(t, mcpOn, model.NATSClient, st)
	out, problems := n.run()
	sec := natsSection(out)
	if problems != 0 || strings.Contains(sec, "FAIL") || strings.Contains(sec, "WARN") {
		t.Fatalf("%d problems:\n%s", problems, sec)
	}
	for _, want := range []string{"MCP server installed (viper-nats-mcp v1)", "registered with Claude Code", "messages of alpha are pushed"} {
		if !strings.Contains(sec, want) {
			t.Errorf("missing %q in\n%s", want, sec)
		}
	}
}

func TestMCPProblemsAreReported(t *testing.T) {
	base := healthyClient + "mcp_bin=ok\nmcp_version=v\nmcp_claude=/b\nmcp_allowed=peers,inbox\nmcp_push=alpha\n"
	for name, tc := range map[string]struct{ status, want string }{
		"no binary":        {healthyClient + "mcp_bin=missing\n", "FAIL  NATS_MCP is on, but"},
		"not registered":   {strings.Replace(base, "mcp_claude=/b", "mcp_claude=none", 1), "FAIL  Claude Code does not know the MCP server nats"},
		"other push list":  {strings.Replace(base, "mcp_push=alpha", "mcp_push=", 1), "WARN  the machines whose messages are pushed"},
		"other allow list": {strings.Replace(base, "mcp_allowed=peers,inbox", "mcp_allowed=", 1), "WARN  the tools of the MCP server allowed"},
	} {
		n := newNATSFixture(t, mcpOn, model.NATSClient, tc.status)
		out, _ := n.run()
		if sec := natsSection(out); !strings.Contains(sec, tc.want) {
			t.Errorf("%s: missing %q in\n%s", name, tc.want, sec)
		}
	}
	// NATS_MCP off: nothing is asked of the machine, a leftover is only mentioned
	n := newNATSFixture(t, "", model.NATSClient, healthyClient+"mcp_bin=ok\nmcp_version=v\n")
	out, _ := n.run()
	if sec := natsSection(out); !strings.Contains(sec, "WARN  the MCP server is installed on the machine, but NATS_MCP is off") {
		t.Errorf("leftover:\n%s", sec)
	}
}
