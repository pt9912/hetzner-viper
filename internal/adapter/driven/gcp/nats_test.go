package gcp_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/gcp"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

const zone = "europe-west3-b"

func natsClient(f *fake) *gcp.Client {
	return gcp.NewWith(gcp.Options{Project: "my-project", Zone: zone, Poll: 1, NATSPort: 4222}, f)
}

func natsSpec(name, network string, labels map[string]string) model.ServerSpec {
	return model.ServerSpec{Name: name, Size: "e2-standard-2", Image: "ubuntu-24.04", SSHKey: "ssh-ed25519 AAAA k", Firewall: "claude-ssh", UserData: "#cloud-config\n", Network: network, Labels: labels}
}

func TestTheFirstStartCreatesTheRuleOfTheGroupAndTheNextOnesFindIt(t *testing.T) {
	f := newFake()
	a, b := natsClient(f), natsClient(f)
	ctx := context.Background()
	n, created, err := a.EnsurePrivateNetwork(ctx, "dev", zone)
	if err != nil || !created || n.ID != "viper-nats-dev" || n.Zone != zone || n.Created.IsZero() {
		t.Fatalf("%+v %v %v", n, created, err)
	}
	r := f.natsRules[n.ID]
	if r.Port != 4222 || r.Tag != "viper-nats-dev" || r.Network != "default" || !strings.Contains(r.Description, "viper-nats-group=dev") {
		t.Errorf("%+v", r)
	}
	n2, created2, err := b.EnsurePrivateNetwork(ctx, "dev", zone)
	if err != nil || created2 || n2.ID != n.ID || n2.Created.IsZero() {
		t.Errorf("%+v %v %v", n2, created2, err)
	}
	if found, err := a.FindPrivateNetwork(ctx, "dev"); err != nil || found == nil || found.ID != n.ID {
		t.Errorf("%+v %v", found, err)
	}
	if none, err := a.FindPrivateNetwork(ctx, "other"); err != nil || none != nil {
		t.Errorf("%+v %v", none, err)
	}
}

func TestARuleOfThatNameThatViperDidNotMakeOrForAnotherPlaceIsNotTaken(t *testing.T) {
	f := newFake()
	f.natsRules["viper-nats-dev"] = &gcp.NATSRule{Name: "viper-nats-dev", Network: "default", Tag: "x", Description: "mine"}
	if _, _, err := natsClient(f).EnsurePrivateNetwork(context.Background(), "dev", zone); err == nil || !strings.Contains(err.Error(), "did not create") {
		t.Errorf("%v", err)
	}
	g := newFake()
	if _, _, err := natsClient(g).EnsurePrivateNetwork(context.Background(), "dev", zone); err != nil {
		t.Fatal(err)
	}
	other := gcp.NewWith(gcp.Options{Project: "my-project", Zone: "europe-west3-c", Network: "default", Poll: 1}, g)
	if _, _, err := other.EnsurePrivateNetwork(context.Background(), "dev", "europe-west3-c"); err == nil || !strings.Contains(err.Error(), "one network and zone") {
		t.Errorf("another zone: %v", err)
	}
	third := gcp.NewWith(gcp.Options{Project: "my-project", Zone: zone, Network: "custom", Poll: 1}, g)
	if _, _, err := third.EnsurePrivateNetwork(context.Background(), "dev", zone); err == nil || !strings.Contains(err.Error(), "one network and zone") {
		t.Errorf("another network: %v", err)
	}
}

func TestNATSNamesAreChecked(t *testing.T) {
	f := newFake()
	if _, _, err := natsClient(f).EnsurePrivateNetwork(context.Background(), "Bad Name", zone); err == nil || len(f.natsRules) != 0 {
		t.Errorf("%v", err)
	}
	if _, err := natsClient(f).NetworkZone(context.Background(), ""); err == nil {
		t.Error("no zone")
	}
}

func TestAServerStartsWithTheTagOfTheGroupAndItsLabels(t *testing.T) {
	f := newFake()
	c := natsClient(f)
	ctx := context.Background()
	n, _, _ := c.EnsurePrivateNetwork(ctx, "dev", zone)
	labels := model.NATSLabels(model.NATSMetadata{Group: "dev", Role: model.NATSServer, State: model.NATSProvisioning})
	srv, err := c.CreateServer(ctx, natsSpec("claude-a", n.ID, labels))
	if err != nil {
		t.Fatal(err)
	}
	if !srv.InNetwork() || srv.PrivateIP == "" {
		t.Errorf("%+v", srv)
	}
	l := f.launches[len(f.launches)-1]
	if l.Tag != "claude-ssh" || len(l.ExtraTags) != 1 || l.ExtraTags[0] != n.ID || l.Labels[model.LabelNATSGroup] != "dev" {
		t.Errorf("%+v", l)
	}
	meta, err := c.NATSMetadataOf(ctx, "claude-a")
	if err != nil || meta == nil || meta.Role != model.NATSServer {
		t.Errorf("%+v %v", meta, err)
	}
	plain, err := c.CreateServer(ctx, natsSpec("claude-b", "", nil))
	if err != nil {
		t.Fatal(err)
	}
	if plain.InNetwork() || plain.PrivateIP == "" {
		t.Errorf("a VM without the tag of the group is not in the network, although it has an address: %+v", plain)
	}
	if m, err := c.NATSMetadataOf(ctx, "claude-b"); err != nil || m != nil {
		t.Errorf("%+v %v", m, err)
	}
}

func TestAttachAddsTheTagOnceAndKeepsTheOthers(t *testing.T) {
	f := newFake()
	c := natsClient(f)
	ctx := context.Background()
	n, _, _ := c.EnsurePrivateNetwork(ctx, "dev", zone)
	if _, err := c.CreateServer(ctx, natsSpec("claude-b", "", nil)); err != nil {
		t.Fatal(err)
	}
	ip, err := c.AttachPrivateNetwork(ctx, "claude-b", n.ID)
	if err != nil || ip == "" || f.tagAdds != 1 {
		t.Fatalf("%q %v %d", ip, err, f.tagAdds)
	}
	if tags := f.instances["claude-b"].Tags; len(tags) != 2 || tags[0] != "claude-ssh" || tags[1] != n.ID {
		t.Errorf("the tag of the SSH firewall stays: %v", tags)
	}
	if again, err := c.AttachPrivateNetwork(ctx, "claude-b", n.ID); err != nil || again != ip || f.tagAdds != 1 {
		t.Errorf("attaching twice changes nothing: %q %v %d", again, err, f.tagAdds)
	}
	if _, err := c.AttachPrivateNetwork(ctx, "nope", n.ID); err == nil {
		t.Error("a VM that is not there")
	}
}

func TestTheGroupListsItsMachinesAndTheLabelsAreMerged(t *testing.T) {
	f := newFake()
	c := natsClient(f)
	ctx := context.Background()
	n, _, _ := c.EnsurePrivateNetwork(ctx, "dev", zone)
	_, _ = c.CreateServer(ctx, natsSpec("claude-a", n.ID, model.NATSLabels(model.NATSMetadata{Group: "dev", Role: model.NATSServer, State: model.NATSProvisioning})))
	_, _ = c.CreateServer(ctx, natsSpec("claude-b", n.ID, model.NATSLabels(model.NATSMetadata{Group: "other", Role: model.NATSServer, State: model.NATSReady})))
	_, _ = c.CreateServer(ctx, natsSpec("claude-c", "", nil))
	if err := c.UpdateNATSMetadata(ctx, "claude-a", model.NATSMetadata{Group: "dev", Role: model.NATSServer, State: model.NATSReady}); err != nil {
		t.Fatal(err)
	}
	ms, err := c.ListGroupMachines(ctx, "dev")
	if err != nil || len(ms) != 1 || ms[0].Server.Name != "claude-a" || ms[0].NATS.State != model.NATSReady || !ms[0].Server.InNetwork() {
		t.Fatalf("%+v %v", ms, err)
	}
	if f.instances["claude-a"].Labels["viper"] != "claude-a" {
		t.Errorf("the other labels stay: %v", f.instances["claude-a"].Labels)
	}
	f.instances["claude-a"].Labels[model.LabelNATSRole] = "boss"
	if _, err := c.ListGroupMachines(ctx, "dev"); err == nil {
		t.Error("a role that does not exist")
	}
}

func TestDeletingTheRuleKeepsWhatIsInUseAndWhatViperDidNotMake(t *testing.T) {
	f := newFake()
	c := natsClient(f)
	ctx := context.Background()
	n, _, _ := c.EnsurePrivateNetwork(ctx, "dev", zone)
	_, _ = c.CreateServer(ctx, natsSpec("claude-a", n.ID, model.NATSLabels(model.NATSMetadata{Group: "dev", Role: model.NATSServer, State: model.NATSReady})))
	if err := c.DeletePrivateNetwork(ctx, n.ID); !errors.Is(err, cloud.ErrNetworkInUse) || len(f.natsRules) != 1 {
		t.Fatalf("a VM of the group exists: %v", err)
	}
	if err := c.DeleteServer(ctx, "claude-a"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeletePrivateNetwork(ctx, n.ID); err != nil || len(f.natsRules) != 0 {
		t.Fatalf("%v", err)
	}
	if err := c.DeletePrivateNetwork(ctx, n.ID); err != nil {
		t.Errorf("a rule that is gone is no error: %v", err)
	}
	f.natsRules["theirs"] = &gcp.NATSRule{Name: "theirs", Description: "not ours"}
	f.natsRules["viper-nats-x"] = &gcp.NATSRule{Name: "viper-nats-x", Description: "no marker"}
	for _, id := range []string{"theirs", "viper-nats-x"} {
		if err := c.DeletePrivateNetwork(ctx, id); err == nil || f.natsRules[id] == nil {
			t.Errorf("%s was deleted or accepted: %v", id, err)
		}
	}
}
