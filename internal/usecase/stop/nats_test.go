package stop_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/natsserver"
	"github.com/pt9912/hetzner-viper/internal/servertest"
	"github.com/pt9912/hetzner-viper/internal/usecase/stop"
)

// natsHz is the Hetzner fake of the stop tests with the NATS capability of the fake cloud.
type natsHz struct {
	hz
	nc *servertest.Cloud
}

func (h natsHz) ListGroupMachines(ctx context.Context, g string) ([]model.GroupMember, error) {
	return h.nc.ListGroupMachines(ctx, g)
}
func (h natsHz) UpdateNATSMetadata(ctx context.Context, id string, m model.NATSMetadata) error {
	return h.nc.UpdateNATSMetadata(ctx, id, m)
}
func (h natsHz) NATSMetadataOf(ctx context.Context, id string) (*model.NATSMetadata, error) {
	return h.nc.NATSMetadataOf(ctx, id)
}
func (h natsHz) NetworkZone(ctx context.Context, l string) (string, error) {
	return h.nc.NetworkZone(ctx, l)
}
func (h natsHz) EnsurePrivateNetwork(ctx context.Context, g, l string) (model.PrivateNetwork, bool, error) {
	return h.nc.EnsurePrivateNetwork(ctx, g, l)
}
func (h natsHz) FindPrivateNetwork(ctx context.Context, g string) (*model.PrivateNetwork, error) {
	return h.nc.FindPrivateNetwork(ctx, g)
}
func (h natsHz) AttachPrivateNetwork(ctx context.Context, s, n string) (string, error) {
	return h.nc.AttachPrivateNetwork(ctx, s, n)
}
func (h natsHz) DeletePrivateNetwork(ctx context.Context, n string) error {
	return h.nc.DeletePrivateNetwork(ctx, n)
}

// DeleteServer deletes in the group fake as well, so the network can go afterwards.
func (h natsHz) DeleteServer(ctx context.Context, id string) error {
	if err := h.hz.DeleteServer(ctx, id); err != nil {
		return err
	}
	for _, s := range h.nc.Servers {
		if s.Name == h.w.server.Name {
			return h.nc.DeleteServer(ctx, s.ID)
		}
	}
	return nil
}

type natsWorld struct {
	*world
	hz       natsHz
	net      model.PrivateNetwork
	conf     string // server.conf at the server
	writes   []string
	readFail error
	cfgFail  error
}

// test keys: a user NKey is "U" and 55 characters of base32
func keyAlpha() string { return "U" + strings.Repeat("A", 55) }
func keyWeb() string   { return "U" + strings.Repeat("B", 55) }
func keyOther() string { return "U" + strings.Repeat("C", 55) }

// newNATSWorld is a group dev with a ready server alpha. The machine being deleted (claude-web)
// is a client in it, or the server when asServer is set.
func newNATSWorld(t *testing.T, role model.NATSRole, others ...string) *natsWorld {
	t.Helper()
	w := newWorld(t)
	w.allLocal()
	n := &natsWorld{world: w, hz: natsHz{hz: hz{w: w}, nc: servertest.NewCloud(time.Now())}}
	ctx := context.Background()
	net, _, err := n.hz.nc.EnsurePrivateNetwork(ctx, "dev", "fsn1")
	if err != nil {
		t.Fatal(err)
	}
	n.net = net
	mk := func(name string, r model.NATSRole) *model.Server {
		s, err := n.hz.nc.CreateServer(ctx, model.ServerSpec{Name: name, Network: net.ID,
			Labels: model.NATSLabels(model.NATSMetadata{Group: "dev", Role: r, State: model.NATSReady})})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	var clients []natsserver.Client
	if role == model.NATSServer {
		w.server = mk("claude-web", model.NATSServer)
		clients = append(clients, natsserver.Client{Machine: "web", PublicKey: keyWeb()})
		for _, o := range others {
			mk("claude-"+o, model.NATSClient)
			clients = append(clients, natsserver.Client{Machine: o, PublicKey: keyOther()})
		}
	} else {
		mk("claude-alpha", model.NATSServer)
		clients = append(clients, natsserver.Client{Machine: "alpha", PublicKey: keyAlpha()})
		w.server = mk("claude-web", model.NATSClient)
		clients = append(clients, natsserver.Client{Machine: "web", PublicKey: keyWeb()})
	}
	w.server.IPv4 = "203.0.113.7"
	conf, err := natsserver.Config{Group: "dev", Host: "10.115.0.2", Port: 4222, Clients: clients}.Render()
	if err != nil {
		t.Fatal(err)
	}
	n.conf = conf
	return n
}

func (n *natsWorld) run(force bool, answers ...string) (int, error) {
	n.t.Helper()
	n.answers = answers
	d := n.deps()
	d.Cloud = n.hz
	inner := d.Run
	d.Run = func(ctx context.Context, stdin, name string, args ...string) (string, string, error) {
		last := args[len(args)-1]
		switch {
		case strings.HasPrefix(last, "bash -c ") && strings.Contains(last, "sha256 none"):
			if n.readFail != nil {
				return "", "", n.readFail
			}
			return "sha256 abc\n" + n.conf, "", nil
		case strings.HasPrefix(last, "bash -c ") && strings.Contains(last, "changed meanwhile"):
			if n.cfgFail != nil {
				return "", "", n.cfgFail
			}
			n.conf = stdin
			n.writes = append(n.writes, stdin)
			return "nats config: written, server reloaded\n", "", nil
		}
		return inner(ctx, stdin, name, args...)
	}
	code, err := stop.Run(context.Background(), n.c, force, d, n.lg, &n.out, &n.errb)
	n.lg.Close(err, code)
	return code, err
}

func (n *natsWorld) registered() []string {
	var out []string
	for _, c := range natsserver.ParseClients(n.conf) {
		out = append(out, c.Machine)
	}
	return out
}

func TestDeletingAClientRevokesItsKeyFirst(t *testing.T) {
	n := newNATSWorld(t, model.NATSClient)
	code, err := n.run(false, "DELETE")
	if code != 0 || err != nil {
		t.Fatalf("%d %v\n%s", code, err, n.all())
	}
	if got := strings.Join(n.registered(), ","); got != "alpha" {
		t.Errorf("registered after: %q", got)
	}
	if len(n.deleted) != 1 {
		t.Errorf("deleted %v", n.deleted)
	}
	has(t, n.all(), "stays until its last machine is deleted", "Server claude-web deleted.")
	if m, _ := n.hz.nc.FindPrivateNetwork(context.Background(), "dev"); m == nil {
		t.Error("the server is still there: the network stays")
	}
	if strings.Contains(n.conf, keyWeb()) {
		t.Error("the key must be gone")
	}
}

func TestAFailedRevokeKeepsTheMachineUnlessForced(t *testing.T) {
	n := newNATSWorld(t, model.NATSClient)
	n.cfgFail = errors.New("exit 255")
	code, _ := n.run(false, "DELETE")
	if code == 0 || len(n.deleted) != 0 {
		t.Fatalf("code %d, deleted %v", code, n.deleted)
	}
	has(t, n.all(), "Revoking the key of web", "NOT deleted", "--force")
	if !strings.Contains(n.conf, keyWeb()) {
		t.Error("the entry is still there, and so is the machine")
	}

	n2 := newNATSWorld(t, model.NATSClient)
	n2.readFail = errors.New("exit 255") // the server cannot be reached at all
	code, err := n2.run(true, "DELETE")
	if code != 0 || err != nil || len(n2.deleted) != 1 {
		t.Fatalf("--force deletes anyway: %d %v %v\n%s", code, err, n2.deleted, n2.all())
	}
	has(t, n2.all(), "WARNING: --force deletes the machine anyway", "stays in the configuration")
}

func TestAClientOfAGroupWithoutServerHasNothingToRevoke(t *testing.T) {
	n := newNATSWorld(t, model.NATSClient)
	ctx := context.Background()
	for _, s := range n.hz.nc.Servers {
		if s.Name == "claude-alpha" {
			_ = n.hz.nc.DeleteServer(ctx, s.ID)
			break
		}
	}
	code, err := n.run(false, "DELETE")
	if code != 0 || err != nil {
		t.Fatalf("%d %v\n%s", code, err, n.all())
	}
	has(t, n.all(), "has no server any more", "Private network viper-nats-dev removed")
	if len(n.writes) != 0 {
		t.Error("nothing to write at a server that is not there")
	}
}

func TestTheServerIsNotDeletedWhileTheGroupHasOtherMachines(t *testing.T) {
	n := newNATSWorld(t, model.NATSServer, "beta", "gamma")
	code, _ := n.run(false, "DELETE")
	if code == 0 || len(n.deleted) != 0 {
		t.Fatalf("code %d, deleted %v", code, n.deleted)
	}
	has(t, n.all(), "is the NATS server of the group dev", "beta, gamma", "Delete them first", "--force")
	if len(n.prompts) != 0 {
		t.Errorf("no question for a refusal: %q", n.prompts)
	}
}

func TestForceEndsTheGroupAfterTheNameIsTyped(t *testing.T) {
	n := newNATSWorld(t, model.NATSServer, "beta")
	code, _ := n.run(true, "wrong", "DELETE")
	if code == 0 || len(n.deleted) != 0 {
		t.Fatalf("a wrong name cancels: %d %v", code, n.deleted)
	}
	has(t, n.all(), "Cancelled")

	n = newNATSWorld(t, model.NATSServer, "beta")
	code, err := n.run(true, "dev", "DELETE")
	if code != 0 || err != nil || len(n.deleted) != 1 {
		t.Fatalf("%d %v %v\n%s", code, err, n.deleted, n.all())
	}
	has(t, n.all(), "the group ends", "stays until its last machine is deleted")
	if len(n.prompts) != 2 || n.prompts[0] != "Type the name of the group (dev) to end it: " {
		t.Errorf("prompts %q", n.prompts)
	}
}

func TestTheLastMachineTakesTheNetworkWithIt(t *testing.T) {
	n := newNATSWorld(t, model.NATSServer) // the server alone
	code, err := n.run(false, "DELETE")
	if code != 0 || err != nil {
		t.Fatalf("%d %v\n%s", code, err, n.all())
	}
	has(t, n.all(), "Private network viper-nats-dev removed (last machine of the group dev)")
	if m, _ := n.hz.nc.FindPrivateNetwork(context.Background(), "dev"); m != nil {
		t.Errorf("the network is left: %+v", m)
	}
}

func TestAMachineWithoutNATSLabelsIsDeletedAsBefore(t *testing.T) {
	n := newNATSWorld(t, model.NATSClient)
	plain, err := n.hz.nc.CreateServer(context.Background(), model.ServerSpec{Name: "claude-plain"})
	if err != nil {
		t.Fatal(err)
	}
	n.server = plain
	code, err := n.run(false, "DELETE")
	if code != 0 || err != nil || len(n.writes) != 0 {
		t.Fatalf("%d %v writes %d\n%s", code, err, len(n.writes), n.all())
	}
	if strings.Contains(n.all(), "NATS") {
		t.Errorf("no word about NATS: %s", n.all())
	}
}

func TestGroupRoleSaysWhatTheMachineIsInItsGroupFromTheLabels(t *testing.T) {
	for _, role := range []model.NATSRole{model.NATSClient, model.NATSServer} {
		n := newNATSWorld(t, role, "beta")
		d := n.deps()
		d.Cloud = n.hz
		got, group, ok, err := stop.Service{Deps: d}.GroupRole(context.Background(), n.c)
		n.lg.Close(nil, 0) // the log file of the world must be closed before the temporary directory goes (Windows)
		if err != nil || !ok || got != role || group != "dev" {
			t.Errorf("%s: %v %q %v %v", role, got, group, ok, err)
		}
	}
	// a machine without labels, or without a server, has no role
	w := newWorld(t)
	d := w.deps()
	_, _, ok, err := (stop.Service{Deps: d}).GroupRole(context.Background(), w.c)
	w.lg.Close(nil, 0)
	if ok || err != nil {
		t.Errorf("no NATS: %v %v", ok, err)
	}
}
