package start_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/servertest"
	"github.com/pt9912/hetzner-viper/internal/usecase/start"
)

// natsHz is the Hetzner fake of the start tests with the NATS capability of the fake cloud.
type natsHz struct {
	hz
	nc        *servertest.Cloud
	createErr error
}

func (h natsHz) CreateServer(ctx context.Context, s model.ServerSpec) (*model.Server, error) {
	if h.createErr != nil {
		return nil, h.createErr
	}
	srv, err := h.hz.CreateServer(ctx, s)
	if err != nil {
		return nil, err
	}
	in, err := h.nc.CreateServer(ctx, model.ServerSpec{Name: s.Name, Labels: s.Labels, Network: s.Network})
	if err != nil {
		return nil, err
	}
	srv.ID, srv.PrivateIP = in.ID, in.PrivateIP
	h.w.createdServer = *srv
	return srv, nil
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

type natsWorld struct {
	*world
	hz natsHz
}

func newNATSWorld(t *testing.T, settings string) *natsWorld {
	t.Helper()
	w := newWorld(t, "NATS_ENABLED=true\nNATS_GROUP=dev\n"+settings)
	return &natsWorld{world: w, hz: natsHz{hz: hz{w: w}, nc: servertest.NewCloud(w.now)}}
}

func (n *natsWorld) run() (int, error) {
	n.t.Helper()
	d := n.deps()
	d.Cloud = n.hz
	code, err := start.Run(context.Background(), n.c, "", d, n.lg, &n.out, &n.err)
	n.lg.Close(err, code)
	return code, err
}

// groupWithServer is a group dev with a ready server alpha.
func (n *natsWorld) groupWithServer() {
	n.t.Helper()
	ctx := context.Background()
	net, _, err := n.hz.nc.EnsurePrivateNetwork(ctx, "dev", "fsn1")
	if err != nil {
		n.t.Fatal(err)
	}
	if _, err := n.hz.nc.CreateServer(ctx, model.ServerSpec{Name: "claude-alpha", Network: net.ID,
		Labels: model.NATSLabels(model.NATSMetadata{Group: "dev", Role: model.NATSServer, State: model.NATSReady})}); err != nil {
		n.t.Fatal(err)
	}
}

func TestTheFirstMachineOfAGroupIsTheServerCandidateAndGetsLabelsAndNetwork(t *testing.T) {
	n := newNATSWorld(t, "")
	code, _ := n.run() // the fake server cannot run the install: it stops after the server was made
	if code == 0 {
		t.Fatalf("the fake cannot run the server install:\n%s", n.all())
	}
	has(t, n.all(), "Private network viper-nats-dev", "NATS group dev: this machine is the server.")
	if len(n.created) != 1 {
		t.Fatalf("servers created: %d", len(n.created))
	}
	s := n.created[0]
	if s.Network == "" || s.Labels[model.LabelNATSGroup] != "dev" || s.Labels[model.LabelNATSRole] != "server" || s.Labels[model.LabelNATSState] != "provisioning" || s.Labels[model.LabelManaged] != "true" {
		t.Errorf("spec %+v", s)
	}
	has(t, n.events(), "nats group=dev role=server")
}

func TestALaterMachineIsAClientOfTheExistingServer(t *testing.T) {
	n := newNATSWorld(t, "")
	n.groupWithServer()
	code, _ := n.run()
	if code == 0 {
		t.Fatalf("clients are not implemented yet:\n%s", n.all())
	}
	has(t, n.all(), "this machine is the client.", "as a client is not implemented yet")
	if s := n.created[0]; s.Labels[model.LabelNATSRole] != "client" || s.Network == "" {
		t.Errorf("spec %+v", s)
	}
	members, _ := n.hz.nc.ListGroupMachines(context.Background(), "dev")
	for _, m := range members {
		if m.Server.Name == "claude-web" && m.NATS.State != model.NATSFailed {
			t.Errorf("the machine that could not join is marked: %+v", m.NATS)
		}
	}
}

func TestAGroupWithANetworkButNoServerIsNotGuessedAbout(t *testing.T) {
	n := newNATSWorld(t, "")
	if _, _, err := n.hz.nc.EnsurePrivateNetwork(context.Background(), "dev", "fsn1"); err != nil {
		t.Fatal(err)
	}
	code, _ := n.run()
	if code == 0 || len(n.created) != 0 {
		t.Fatalf("code %d, %d servers created", code, len(n.created))
	}
	has(t, n.all(), "has a network but no server", "viper-nats-dev")
}

func TestAMachineOfAnotherGroupIsNotMovedSilently(t *testing.T) {
	n := newNATSWorld(t, "")
	ctx := context.Background()
	other, err := n.hz.nc.CreateServer(ctx, model.ServerSpec{Name: "claude-web", Labels: model.NATSLabels(model.NATSMetadata{Group: "prod", Role: model.NATSClient, State: model.NATSReady})})
	if err != nil {
		t.Fatal(err)
	}
	n.server = other
	code, _ := n.run()
	if code == 0 || len(n.created) != 0 {
		t.Fatalf("code %d", code)
	}
	has(t, n.all(), "belongs to the NATS group prod, not dev")
	if m, _ := n.hz.nc.FindPrivateNetwork(ctx, "dev"); m != nil {
		t.Error("no network may be made for a refused machine")
	}
}

func TestTheNetworkIsRemovedAgainIfTheServerCannotBeCreated(t *testing.T) {
	n := newNATSWorld(t, "")
	n.hz.createErr = errors.New("quota")
	code, _ := n.run()
	if code == 0 {
		t.Fatal("must fail")
	}
	has(t, n.all(), "creating the server", "Private network viper-nats-dev removed again")
	if m, _ := n.hz.nc.FindPrivateNetwork(context.Background(), "dev"); m != nil {
		t.Errorf("the network was left behind: %+v", m)
	}
}

func TestNATSNeedsAProviderThatCanDoIt(t *testing.T) {
	w := newWorld(t, "NATS_ENABLED=true\nNATS_GROUP=dev\n")
	code, _ := w.run("") // hz is the plain Hetzner fake of the other tests
	if code == 0 || !strings.Contains(w.all(), "NATS groups are not supported with Hetzner") {
		t.Fatalf("%d\n%s", code, w.all())
	}
	if len(w.created) != 0 {
		t.Error("nothing may be created")
	}
}

func TestWithoutNATSNothingChanges(t *testing.T) {
	w := newWorld(t, "")
	if code, err := w.run(""); code != 0 || err != nil {
		t.Fatalf("%d %v\n%s", code, err, w.all())
	}
	if s := w.created[0]; s.Labels != nil || s.Network != "" {
		t.Errorf("no labels and no network without NATS: %+v", s)
	}
}
