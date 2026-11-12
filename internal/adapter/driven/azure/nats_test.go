package azure_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/azure"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

const region = "germanywestcentral"

func natsSpec(name, network string, labels map[string]string) model.ServerSpec {
	return model.ServerSpec{Name: name, Size: "Standard_D2s_v5", Image: "ubuntu-24.04", SSHKey: "ssh-ed25519 AAAA k", Firewall: "claude-ssh", UserData: "#cloud-config\n", Network: network, Labels: labels}
}

func TestTheFirstStartMakesTheApplicationSecurityGroupAndTheNextOnesFindIt(t *testing.T) {
	f := newFake()
	a, b := client(f), client(f)
	ctx := context.Background()
	n, created, err := a.EnsurePrivateNetwork(ctx, "dev", region)
	if err != nil || !created || n.ID != "viper-nats-dev" || n.Zone != region {
		t.Fatalf("%+v %v %v", n, created, err)
	}
	n2, created2, err := b.EnsurePrivateNetwork(ctx, "dev", region)
	if err != nil || created2 || n2.ID != n.ID {
		t.Errorf("the second caller is not the first: %+v %v %v", n2, created2, err)
	}
	if found, err := a.FindPrivateNetwork(ctx, "dev"); err != nil || found == nil || found.ID != n.ID {
		t.Errorf("%+v %v", found, err)
	}
	if none, err := a.FindPrivateNetwork(ctx, "other"); err != nil || none != nil {
		t.Errorf("%+v %v", none, err)
	}
}

func TestAGroupThatViperDidNotMakeOrInAnotherRegionIsNotTaken(t *testing.T) {
	f := newFake()
	f.asgs["viper-nats-dev"] = &azure.GroupASG{Name: "viper-nats-dev", Location: region, Tags: map[string]string{}}
	if _, _, err := client(f).EnsurePrivateNetwork(context.Background(), "dev", region); err == nil {
		t.Error("a group of that name that viper did not make")
	}
	f = newFake()
	if _, _, err := client(f).EnsurePrivateNetwork(context.Background(), "dev", region); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client(f).EnsurePrivateNetwork(context.Background(), "dev", "westeurope"); err == nil {
		t.Error("another region")
	}
	if _, _, err := client(f).EnsurePrivateNetwork(context.Background(), "a.b", region); err == nil {
		t.Error("a name with a dot")
	}
}

func TestAServerStartsInTheGroupWithItsTagsAndTheRule(t *testing.T) {
	f := newFake()
	c := client(f)
	prepare(t, f, c)
	ctx := context.Background()
	n, _, err := c.EnsurePrivateNetwork(ctx, "dev", region)
	if err != nil {
		t.Fatal(err)
	}
	labels := model.NATSLabels(model.NATSMetadata{Group: "dev", Role: model.NATSServer, State: model.NATSProvisioning})
	s, err := c.CreateServer(ctx, natsSpec("claude-alpha", n.ID, labels))
	if err != nil {
		t.Fatal(err)
	}
	if !s.InNetwork() || s.PrivateIP == "" {
		t.Errorf("%+v", s)
	}
	if !f.groupNS["claude-ssh dev"] || f.launches[0].Group == "" || f.launches[0].Tags[model.LabelNATSRole] != "server" {
		t.Errorf("%+v %v", f.launches[0], f.groupNS)
	}
	meta, err := c.NATSMetadataOf(ctx, "claude-alpha")
	if err != nil || meta == nil || meta.Role != model.NATSServer {
		t.Errorf("%+v %v", meta, err)
	}
	if err := c.UpdateNATSMetadata(ctx, "claude-alpha", model.NATSMetadata{Group: "dev", Role: model.NATSServer, State: model.NATSReady}); err != nil {
		t.Fatal(err)
	}
	ms, err := c.ListGroupMachines(ctx, "dev")
	if err != nil || len(ms) != 1 || ms[0].NATS.State != model.NATSReady || ms[0].Server.Name != "claude-alpha" {
		t.Errorf("%+v %v", ms, err)
	}
}

func TestAServerWithoutTheGroupIsNotInTheNetwork(t *testing.T) {
	f := newFake()
	c := client(f)
	prepare(t, f, c)
	ctx := context.Background()
	s, err := c.CreateServer(ctx, natsSpec("claude-web", "", nil))
	if err != nil {
		t.Fatal(err)
	}
	if s.InNetwork() || s.PrivateIP == "" {
		t.Errorf("an address, but not in the group: %+v", s)
	}
	n, _, _ := c.EnsurePrivateNetwork(ctx, "dev", region)
	ip, err := c.AttachPrivateNetwork(ctx, "claude-web", n.ID)
	if err != nil || ip != s.PrivateIP || !f.groupNS["claude-ssh dev"] {
		t.Errorf("%q %v %v", ip, err, f.groupNS)
	}
}

func TestDeletingTheNetworkKeepsWhatIsInUseAndWhatViperDidNotMake(t *testing.T) {
	f := newFake()
	c := client(f)
	prepare(t, f, c)
	ctx := context.Background()
	n, _, _ := c.EnsurePrivateNetwork(ctx, "dev", region)
	labels := model.NATSLabels(model.NATSMetadata{Group: "dev", Role: model.NATSServer, State: model.NATSReady})
	if _, err := c.CreateServer(ctx, natsSpec("claude-alpha", n.ID, labels)); err != nil {
		t.Fatal(err)
	}
	if err := c.DeletePrivateNetwork(ctx, n.ID); !errors.Is(err, cloud.ErrNetworkInUse) {
		t.Errorf("a VM is still there: %v", err)
	}
	_ = c.DeleteServer(ctx, "claude-alpha")
	f.inUse = true
	if err := c.DeletePrivateNetwork(ctx, n.ID); !errors.Is(err, cloud.ErrNetworkInUse) {
		t.Errorf("Azure says it is in use: %v", err)
	}
	f.inUse = false
	if err := c.DeletePrivateNetwork(ctx, n.ID); err != nil || len(f.asgs) != 0 || len(f.groupNS) != 0 {
		t.Errorf("%v %v %v", err, f.asgs, f.groupNS)
	}
	f.asgs["viper-nats-x"] = &azure.GroupASG{Name: "viper-nats-x", Tags: map[string]string{}}
	if err := c.DeletePrivateNetwork(ctx, "viper-nats-x"); err == nil || len(f.asgs) != 1 {
		t.Errorf("not ours: %v", err)
	}
}
