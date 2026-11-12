package aws_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/aws"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

func natsClient(f *fakeAWS) *aws.Client {
	return aws.NewWith(aws.Options{Location: "eu-central-1", Poll: 1, NATSPort: 4222}, f, f)
}

func natsSpec(name, network string, labels map[string]string) model.ServerSpec {
	return model.ServerSpec{Name: name, Size: "t4g.small", Region: "eu-central-1", Image: "ubuntu-24.04", SSHKey: "claude-login", Firewall: "claude-ssh", Network: network, Labels: labels}
}

func TestTheFirstStartCreatesTheSecurityGroupOfTheGroupAndTheNextOnesFindIt(t *testing.T) {
	f := newFakeAWS()
	a, b := natsClient(f), natsClient(f)
	ctx := context.Background()
	n, created, err := a.EnsurePrivateNetwork(ctx, "dev", "eu-central-1a")
	if err != nil || !created || n.Name != "viper-nats-dev" || n.Zone != "eu-central-1" || n.ID == "" || n.Created.IsZero() {
		t.Fatalf("%+v %v %v", n, created, err)
	}
	g := f.groups[n.ID]
	if len(g.perms) != 1 || *g.perms[0].FromPort != 4222 || *g.perms[0].ToPort != 4222 || *g.perms[0].IpProtocol != "tcp" ||
		len(g.perms[0].UserIdGroupPairs) != 1 || *g.perms[0].UserIdGroupPairs[0].GroupId != n.ID || len(g.perms[0].IpRanges) != 0 {
		t.Errorf("the rule lets in the port from the group and from nobody else: %+v", g.perms)
	}
	n2, created2, err := b.EnsurePrivateNetwork(ctx, "dev", "eu-central-1a")
	if err != nil || created2 || n2.ID != n.ID || n2.Created.IsZero() {
		t.Errorf("the second caller is not the first: %+v %v %v", n2, created2, err)
	}
	if len(f.groups) != 1 || len(g.perms) != 1 {
		t.Errorf("groups %d, rules %d", len(f.groups), len(g.perms))
	}
	found, err := a.FindPrivateNetwork(ctx, "dev")
	if err != nil || found == nil || found.ID != n.ID {
		t.Errorf("%+v %v", found, err)
	}
	if none, err := a.FindPrivateNetwork(ctx, "other"); err != nil || none != nil {
		t.Errorf("%+v %v", none, err)
	}
}

func TestAGroupWithTheRuleMissingIsRepairedByTheNextCaller(t *testing.T) {
	f := newFakeAWS()
	c := natsClient(f)
	n, _, _ := c.EnsurePrivateNetwork(context.Background(), "dev", "")
	f.groups[n.ID].perms = nil // the first start stopped between creating the group and the rule
	if _, created, err := c.EnsurePrivateNetwork(context.Background(), "dev", ""); err != nil || created || len(f.groups[n.ID].perms) != 1 {
		t.Errorf("%v %v %d", err, created, len(f.groups[n.ID].perms))
	}
}

func TestASecurityGroupOfThatNameThatViperDidNotMakeIsNotTaken(t *testing.T) {
	f := newFakeAWS()
	f.groups["sg-9"] = &group{name: "viper-nats-dev", vpc: "vpc-1"}
	_, _, err := natsClient(f).EnsurePrivateNetwork(context.Background(), "dev", "")
	if err == nil || !strings.Contains(err.Error(), "did not create") || len(f.groups["sg-9"].perms) != 0 {
		t.Errorf("%v", err)
	}
}

func TestAGroupInAnotherVPCIsRefusedBeforeAnythingIsCreated(t *testing.T) {
	f := newFakeAWS()
	f.groups["sg-9"] = &group{name: "viper-nats-dev", vpc: "vpc-2", tags: []types.Tag{
		{Key: ptr(model.LabelManaged), Value: ptr("true")}, {Key: ptr(model.LabelNATSGroup), Value: ptr("dev")}}}
	_, created, err := natsClient(f).EnsurePrivateNetwork(context.Background(), "dev", "")
	if err == nil || created || !strings.Contains(err.Error(), "one VPC") || len(f.created) != 0 {
		t.Errorf("%v %v %d", err, created, len(f.created))
	}
}

func TestNATSNamesAreChecked(t *testing.T) {
	f := newFakeAWS()
	c := natsClient(f)
	if _, _, err := c.EnsurePrivateNetwork(context.Background(), "Bad Name", ""); err == nil || len(f.created) != 0 {
		t.Errorf("%v", err)
	}
}

func TestAServerStartsInTheNetworkOfTheGroupWithItsLabels(t *testing.T) {
	f := newFakeAWS()
	c := natsClient(f)
	prepare(t, f, c)
	ctx := context.Background()
	n, _, _ := c.EnsurePrivateNetwork(ctx, "dev", "")
	labels := model.NATSLabels(model.NATSMetadata{Group: "dev", Role: model.NATSServer, State: model.NATSProvisioning})
	srv, err := c.CreateServer(ctx, natsSpec("claude-a", n.ID, labels))
	if err != nil {
		t.Fatal(err)
	}
	if !srv.InNetwork() || srv.PrivateIP == "" {
		t.Errorf("%+v", srv)
	}
	groups := f.runs[len(f.runs)-1].NetworkInterfaces[0].Groups
	if len(groups) != 2 || groups[1] != n.ID {
		t.Errorf("groups %v", groups)
	}
	meta, err := c.NATSMetadataOf(ctx, srv.ID)
	if err != nil || meta == nil || meta.Group != "dev" || meta.Role != model.NATSServer || meta.State != model.NATSProvisioning {
		t.Errorf("%+v %v", meta, err)
	}
	// a server of the same project that is not in a group
	plain, err := c.CreateServer(ctx, natsSpec("claude-b", "", nil))
	if err != nil {
		t.Fatal(err)
	}
	if plain.InNetwork() || plain.PrivateIP == "" {
		t.Errorf("a server without the rule of the group is not in the network, although it has an address: %+v", plain)
	}
	if m, err := c.NATSMetadataOf(ctx, plain.ID); err != nil || m != nil {
		t.Errorf("%+v %v", m, err)
	}
}

func TestAttachAddsTheGroupAndKeepsTheOthers(t *testing.T) {
	f := newFakeAWS()
	c := natsClient(f)
	prepare(t, f, c)
	ctx := context.Background()
	n, _, _ := c.EnsurePrivateNetwork(ctx, "dev", "")
	plain, err := c.CreateServer(ctx, natsSpec("claude-b", "", nil))
	if err != nil {
		t.Fatal(err)
	}
	ip, err := c.AttachPrivateNetwork(ctx, plain.ID, n.ID)
	if err != nil || ip != plain.PrivateIP {
		t.Fatalf("%q %v", ip, err)
	}
	if len(f.modified) != 1 || len(f.modified[0].Groups) != 2 || f.modified[0].Groups[1] != n.ID {
		t.Fatalf("the list replaces all groups: %+v", f.modified)
	}
	if again, err := c.AttachPrivateNetwork(ctx, plain.ID, n.ID); err != nil || again != ip || len(f.modified) != 1 {
		t.Errorf("attaching twice changes nothing: %q %v %d", again, err, len(f.modified))
	}
	got, _ := c.ServerByName(ctx, "claude-b")
	if got == nil || !got.InNetwork() {
		t.Errorf("%+v", got)
	}
	if _, err := c.AttachPrivateNetwork(ctx, "i-9999", n.ID); err == nil {
		t.Error("an instance that is not there")
	}
}

func TestTheGroupListsItsMachinesAndTheTagsAreMerged(t *testing.T) {
	f := newFakeAWS()
	c := natsClient(f)
	prepare(t, f, c)
	ctx := context.Background()
	n, _, _ := c.EnsurePrivateNetwork(ctx, "dev", "")
	a, _ := c.CreateServer(ctx, natsSpec("claude-a", n.ID, model.NATSLabels(model.NATSMetadata{Group: "dev", Role: model.NATSServer, State: model.NATSProvisioning})))
	_, _ = c.CreateServer(ctx, natsSpec("claude-b", n.ID, model.NATSLabels(model.NATSMetadata{Group: "other", Role: model.NATSServer, State: model.NATSReady})))
	_, _ = c.CreateServer(ctx, natsSpec("claude-c", "", nil))
	if err := c.UpdateNATSMetadata(ctx, a.ID, model.NATSMetadata{Group: "dev", Role: model.NATSServer, State: model.NATSReady}); err != nil {
		t.Fatal(err)
	}
	ms, err := c.ListGroupMachines(ctx, "dev")
	if err != nil || len(ms) != 1 || ms[0].Server.Name != "claude-a" || ms[0].NATS.State != model.NATSReady || !ms[0].Server.InNetwork() {
		t.Fatalf("%+v %v", ms, err)
	}
	inst := f.instances[a.ID]
	if tagOf(inst, "viper") != "claude-a" || tagOf(inst, "Name") == "" {
		t.Errorf("the other tags stay: %+v", inst.Tags)
	}
	// tags that make no sense are an error
	f.instances[a.ID].Tags = append(f.instances[a.ID].Tags, types.Tag{Key: ptr(model.LabelNATSRole), Value: ptr("boss")})
	if _, err := c.ListGroupMachines(ctx, "dev"); err == nil {
		t.Error("a role that does not exist")
	}
}

func TestDeletingTheNetworkWaitsForTheInterfaceAndKeepsWhatIsInUse(t *testing.T) {
	f := newFakeAWS()
	c := natsClient(f)
	prepare(t, f, c)
	ctx := context.Background()
	n, _, _ := c.EnsurePrivateNetwork(ctx, "dev", "")
	srv, _ := c.CreateServer(ctx, natsSpec("claude-a", n.ID, nil))
	if err := c.DeletePrivateNetwork(ctx, n.ID); !errors.Is(err, cloud.ErrNetworkInUse) || len(f.deleted) != 0 {
		t.Fatalf("an instance still has it: %v", err)
	}
	if err := c.DeleteServer(ctx, srv.ID); err != nil {
		t.Fatal(err)
	}
	f.busyDeletes = 3 // AWS still counts the interface of the instance that just ended
	if err := c.DeletePrivateNetwork(ctx, n.ID); err != nil || len(f.deleted) != 1 {
		t.Fatalf("%v %v", err, f.deleted)
	}
	if err := c.DeletePrivateNetwork(ctx, n.ID); err != nil {
		t.Errorf("a group that is gone is no error: %v", err)
	}
}

func TestOnlyASecurityGroupOfViperIsDeleted(t *testing.T) {
	f := newFakeAWS()
	f.groups["sg-7"] = &group{name: "theirs", vpc: "vpc-1"}
	f.groups["sg-8"] = &group{name: "claude-ssh", vpc: "vpc-1", tags: []types.Tag{{Key: ptr("viper"), Value: ptr("claude-ssh")}}}
	c := natsClient(f)
	for _, id := range []string{"sg-7", "sg-8"} {
		if err := c.DeletePrivateNetwork(context.Background(), id); err == nil || len(f.deleted) != 0 {
			t.Errorf("%s was deleted or accepted: %v", id, err)
		}
	}
}

func TestTheZoneOfALocationIsItsRegion(t *testing.T) {
	c := natsClient(newFakeAWS())
	for in, want := range map[string]string{"eu-central-1a": "eu-central-1", "eu-central-1": "eu-central-1", "us-east-2c": "us-east-2"} {
		if got, err := c.NetworkZone(context.Background(), in); err != nil || got != want {
			t.Errorf("%s: %q %v", in, got, err)
		}
	}
}

func TestARuleThatSomebodyChangedIsReported(t *testing.T) {
	f := newFakeAWS()
	c := natsClient(f)
	ctx := context.Background()
	n, _, err := c.EnsurePrivateNetwork(ctx, "dev", "eu-central-1a")
	if err != nil {
		t.Fatal(err)
	}
	if found, err := c.FindPrivateNetwork(ctx, "dev"); err != nil || len(found.Problems) != 0 {
		t.Fatalf("as made: %+v %v", found, err)
	}
	g := f.groups[n.ID]
	g.perms = append(g.perms, types.IpPermission{IpProtocol: ptr("tcp"), FromPort: ptr(int32(4222)), ToPort: ptr(int32(4222)), IpRanges: []types.IpRange{{CidrIp: ptr("0.0.0.0/0")}}})
	g.perms = append(g.perms, types.IpPermission{IpProtocol: ptr("tcp"), FromPort: ptr(int32(22)), ToPort: ptr(int32(22))})
	found, err := c.FindPrivateNetwork(ctx, "dev")
	if err != nil || len(found.Problems) != 2 || !strings.Contains(found.Problems[0], "outside the group") {
		t.Errorf("%+v %v", found, err)
	}
	g.perms = nil
	found, _ = c.FindPrivateNetwork(ctx, "dev")
	if len(found.Problems) != 1 || !strings.Contains(found.Problems[0], "no rule") {
		t.Errorf("%+v", found)
	}
}
