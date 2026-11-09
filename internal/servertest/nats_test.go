package servertest_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
	"github.com/pt9912/hetzner-viper/internal/servertest"
)

func TestFakeCloudHoldsNATSGroups(t *testing.T) {
	c := servertest.NewCloud(time.Now())
	ctx := context.Background()
	mk := func(name, group string, role model.NATSRole) *model.Server {
		s, err := c.CreateServer(ctx, model.ServerSpec{Name: name, Labels: model.NATSLabels(model.NATSMetadata{Group: group, Role: role, State: model.NATSProvisioning})})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	a := mk("claude-alpha", "dev", model.NATSServer)
	mk("claude-beta", "dev", model.NATSClient)
	mk("claude-other", "prod", model.NATSServer)
	if _, err := c.CreateServer(ctx, model.ServerSpec{Name: "claude-plain"}); err != nil {
		t.Fatal(err)
	}
	got, err := c.ListGroupMachines(ctx, "dev")
	if err != nil || len(got) != 2 || got[0].Server.Name != "claude-alpha" || got[1].Server.Name != "claude-beta" {
		t.Fatalf("%+v %v", got, err)
	}
	if err := c.UpdateNATSMetadata(ctx, a.ID, model.NATSMetadata{Group: "dev", Role: model.NATSServer, State: model.NATSReady}); err != nil {
		t.Fatal(err)
	}
	got, _ = c.ListGroupMachines(ctx, "dev")
	if got[0].NATS.State != model.NATSReady || got[1].NATS.State != model.NATSProvisioning {
		t.Errorf("%+v", got)
	}
	if err := c.DeleteServer(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ = c.ListGroupMachines(ctx, "dev"); len(got) != 1 {
		t.Errorf("a deleted server leaves the group: %+v", got)
	}
}

func TestOnlyOneOfSeveralFirstStartsCreatesTheNetwork(t *testing.T) {
	c := servertest.NewCloud(time.Now())
	ctx := context.Background()
	const n = 20
	var wg sync.WaitGroup
	created := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			net, made, err := c.EnsurePrivateNetwork(ctx, "dev", "fsn1")
			if err != nil || net.Name != "viper-nats-dev" {
				t.Errorf("%+v %v", net, err)
			}
			created <- made
		}()
	}
	wg.Wait()
	close(created)
	count := 0
	for made := range created {
		if made {
			count++
		}
	}
	if count != 1 {
		t.Errorf("%d callers created the network, want exactly one", count)
	}
}

func TestFakeNetworkZoneAndLifecycle(t *testing.T) {
	c := servertest.NewCloud(time.Now())
	ctx := context.Background()
	net, made, err := c.EnsurePrivateNetwork(ctx, "dev", "fsn1")
	if err != nil || !made || net.Zone != "eu-central" {
		t.Fatalf("%+v %v %v", net, made, err)
	}
	if _, _, err := c.EnsurePrivateNetwork(ctx, "dev", "nbg1"); err != nil {
		t.Errorf("nbg1 is in the same zone: %v", err)
	}
	if _, _, err := c.EnsurePrivateNetwork(ctx, "dev", "ash"); err == nil {
		t.Error("ash is in another zone")
	}
	s, err := c.CreateServer(ctx, model.ServerSpec{Name: "claude-alpha", Network: net.ID})
	if err != nil || s.PrivateIP == "" {
		t.Fatalf("a server created into the network has a private IP: %+v %v", s, err)
	}
	plain, _ := c.CreateServer(ctx, model.ServerSpec{Name: "claude-beta"})
	ip, err := c.AttachPrivateNetwork(ctx, plain.ID, net.ID)
	if err != nil || ip == "" || ip == s.PrivateIP {
		t.Fatalf("attach: %q %v", ip, err)
	}
	if again, _ := c.AttachPrivateNetwork(ctx, plain.ID, net.ID); again != ip {
		t.Errorf("attached already: %q, want %q", again, ip)
	}
	if err := c.DeletePrivateNetwork(ctx, net.ID); !errors.Is(err, cloud.ErrNetworkInUse) {
		t.Errorf("servers are attached: %v", err)
	}
	_ = c.DeleteServer(ctx, s.ID)
	_ = c.DeleteServer(ctx, plain.ID)
	if err := c.DeletePrivateNetwork(ctx, net.ID); err != nil {
		t.Errorf("no server left: %v", err)
	}
	if n, _ := c.FindPrivateNetwork(ctx, "dev"); n != nil {
		t.Errorf("deleted: %+v", n)
	}
}
