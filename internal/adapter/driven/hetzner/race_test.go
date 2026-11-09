package hetzner_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/hetzner"
	"github.com/pt9912/hetzner-viper/internal/model"
)

const racePub = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGJ8bCwY3F3d6Y0dP2h9sZxk4m3q0Yy2j2yKpYq1x1aB me"

// keysThatLoseTheRace is a project where somebody else uploads the key between our look and our upload.
type keysThatLoseTheRace struct {
	lists  int
	create error
}

func (k *keysThatLoseTheRace) SSHKeys(context.Context) ([]hetzner.SSHKey, error) {
	k.lists++
	if k.lists == 1 {
		return nil, nil
	}
	return []hetzner.SSHKey{{Name: "someone-login", Fingerprint: model.FingerprintMD5(racePub)}}, nil
}

func (k *keysThatLoseTheRace) SSHKeyCreate(context.Context, string, string) error { return k.create }

func TestEnsureLoginKeyUsesTheKeyAnotherRunUploadedAtTheSameMoment(t *testing.T) {
	k := &keysThatLoseTheRace{create: errors.New("uniqueness_error")}
	got, err := hetzner.EnsureLoginKey(context.Background(), k, "claude-login", racePub)
	if err != nil || got.Ref != "someone-login" || got.Created {
		t.Fatalf("%+v %v", got, err)
	}
	// a failure that is not a lost race stays a failure
	if _, err := hetzner.EnsureLoginKey(context.Background(), &failingKeys{}, "claude-login", racePub); err == nil {
		t.Error("a real failure must be reported")
	}
}

type failingKeys struct{}

func (failingKeys) SSHKeys(context.Context) ([]hetzner.SSHKey, error) { return nil, nil }
func (failingKeys) SSHKeyCreate(context.Context, string, string) error {
	return errors.New("forbidden")
}

// firewallsThatLoseTheRace: FirewallRules says there is none, then the create fails because there is one.
type firewallsThatLoseTheRace struct {
	reads int
	added []hetzner.FirewallRule
}

func (f *firewallsThatLoseTheRace) FirewallRules(context.Context, string) ([]hetzner.FirewallRule, bool, error) {
	f.reads++
	if f.reads == 1 {
		return nil, false, nil
	}
	return []hetzner.FirewallRule{{Direction: "in", Protocol: "tcp", Port: "22"}, {Direction: "in", Protocol: "icmp"}}, true, nil
}
func (f *firewallsThatLoseTheRace) FirewallCreate(context.Context, string) error {
	return errors.New("uniqueness_error")
}
func (f *firewallsThatLoseTheRace) FirewallAddRule(_ context.Context, _ string, r hetzner.FirewallRule) error {
	f.added = append(f.added, r)
	return nil
}

func TestEnsureFirewallUsesTheFirewallAnotherRunCreatedAtTheSameMoment(t *testing.T) {
	f := &firewallsThatLoseTheRace{}
	res, err := hetzner.EnsureFirewall(context.Background(), f, "claude-ssh", []string{"0.0.0.0/0"})
	if err != nil || res.Created || !res.Complete || len(f.added) != 0 {
		t.Fatalf("%+v %v added %v", res, err, f.added)
	}
	// the create fails and the firewall is still not there: the failure is reported
	g := &brokenFirewalls{}
	if _, err := hetzner.EnsureFirewall(context.Background(), g, "claude-ssh", nil); err == nil {
		t.Error("a real failure must be reported")
	}
}

type brokenFirewalls struct{}

func (brokenFirewalls) FirewallRules(context.Context, string) ([]hetzner.FirewallRule, bool, error) {
	return nil, false, nil
}
func (brokenFirewalls) FirewallCreate(context.Context, string) error { return errors.New("forbidden") }
func (brokenFirewalls) FirewallAddRule(context.Context, string, hetzner.FirewallRule) error {
	return nil
}
