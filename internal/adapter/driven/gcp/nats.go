package gcp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

// NATS groups on Google Cloud (docs/plan/nats-anbieter.md §3.3). The "private network" of a group is the
// VPC network of the VMs plus the firewall rule viper-nats-<group>: it lets the NATS port in from the VMs
// with the network tag viper-nats-<group> to those VMs. Creating the rule is the lock of the first start:
// a rule name is unique in a project, so a second create is refused and only the first caller becomes the
// server candidate. No network or subnet is made or changed. The NATS labels are labels of the VM.
//
// The rule only adds an allow: networks that allow all traffic inside (the default network has
// default-allow-internal) are open to the port with or without it. What protects the port is the NATS server
// itself (it listens on the private address only, registered keys only, the certificate the clients trust).

var _ cloud.NATSProvider = (*Client)(nil)

const natsPrefix = "viper-nats-"

// RuleName is the name of the firewall rule (and the network tag) of a NATS group.
func RuleName(group string) string { return natsPrefix + group }

func (o Options) natsPort() int {
	if o.NATSPort > 0 {
		return o.NATSPort
	}
	return model.DefaultNATSPort
}

// networkTags are the extra network tags of a new VM: the one of its NATS group.
func networkTags(network string) []string {
	if network == "" {
		return nil
	}
	return []string{network}
}

// inNATSGroup tells whether the VM has the network tag of a NATS group.
func inNATSGroup(i *Instance) bool {
	for _, t := range i.Tags {
		if strings.HasPrefix(t, natsPrefix) {
			return true
		}
	}
	return false
}

// A rule has no labels, so its description says whose it is, in which zone and since when.
var markerRe = regexp.MustCompile(`viper-managed=true viper-nats-group=([a-z0-9-]+) zone=(\S+) created=(\S+)`)

func marker(group, zone string, now time.Time) string {
	return fmt.Sprintf("viper: NATS port of the group %s. viper-managed=true viper-nats-group=%s zone=%s created=%s", group, group, zone, now.UTC().Format(time.RFC3339))
}

// markerOf reads the description of a rule; ok is false if viper did not write it.
func markerOf(r *NATSRule) (group, zone string, created time.Time, ok bool) {
	m := markerRe.FindStringSubmatch(r.Description)
	if m == nil {
		return "", "", time.Time{}, false
	}
	t, _ := time.Parse(time.RFC3339, m[3])
	return m[1], m[2], t, true
}

// NetworkZone is the zone of the location: the VMs of a group must be in one zone (the labels are read per zone).
func (*Client) NetworkZone(_ context.Context, location string) (string, error) {
	if location == "" {
		return "", errors.New("no zone: set SERVER_LOCATION")
	}
	return location, nil
}

func networkOf(r *NATSRule, zone string, created time.Time) *model.PrivateNetwork {
	return &model.PrivateNetwork{ID: r.Name, Name: r.Name, IPRange: "", Zone: zone, Created: created}
}

// EnsurePrivateNetwork creates the rule of the group, or finds it; see cloud.NATSProvider.
func (c *Client) EnsurePrivateNetwork(ctx context.Context, group, location string) (model.PrivateNetwork, bool, error) {
	if err := model.CheckNATSName("NATS group", group); err != nil {
		return model.PrivateNetwork{}, false, err
	}
	api, err := c.client(ctx)
	if err != nil {
		return model.PrivateNetwork{}, false, err
	}
	zone, err := c.NetworkZone(ctx, location)
	if err != nil {
		return model.PrivateNetwork{}, false, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	want := NATSRule{Name: RuleName(group), Network: c.opts.network(), Tag: RuleName(group), Port: c.opts.natsPort(), Description: marker(group, zone, now)}
	err = api.CreateNATSFirewall(ctx, want)
	switch {
	case err == nil:
		return *networkOf(&want, zone, now), true, nil
	case !errors.Is(err, ErrExists):
		return model.PrivateNetwork{}, false, &cloud.OpError{Op: fmt.Sprintf("creating the firewall rule %s", want.Name), Write: true, Err: err}
	}
	// somebody was first: use the rule, if it is the group's, in this network and zone
	have, err := api.NATSFirewall(ctx, want.Name)
	if err != nil {
		return model.PrivateNetwork{}, false, err
	}
	if have == nil {
		return model.PrivateNetwork{}, false, fmt.Errorf("the firewall rule %s was refused as existing but cannot be found: try again", want.Name)
	}
	g, z, created, ok := markerOf(have)
	if !ok || g != group {
		return model.PrivateNetwork{}, false, fmt.Errorf("a firewall rule named %s exists that viper did not create for the group %s", want.Name, group)
	}
	if have.Network != want.Network || z != zone {
		return model.PrivateNetwork{}, false, fmt.Errorf("the NATS group %s exists in the network %s, zone %s; this machine goes to %s, %s: all machines of a group must be in one network and zone", group, have.Network, z, want.Network, zone)
	}
	return *networkOf(have, z, created), false, nil
}

// FindPrivateNetwork returns the rule of the group, nil if there is none.
func (c *Client) FindPrivateNetwork(ctx context.Context, group string) (*model.PrivateNetwork, error) {
	if err := model.CheckNATSName("NATS group", group); err != nil {
		return nil, err
	}
	api, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	r, err := api.NATSFirewall(ctx, RuleName(group))
	if err != nil || r == nil {
		return nil, err
	}
	g, zone, created, ok := markerOf(r)
	if !ok || g != group {
		return nil, nil
	}
	return networkOf(r, zone, created), nil
}

// AttachPrivateNetwork adds the network tag of the group to the VM and returns its private address.
func (c *Client) AttachPrivateNetwork(ctx context.Context, serverID, networkID string) (string, error) {
	api, err := c.client(ctx)
	if err != nil {
		return "", err
	}
	i, err := api.Instance(ctx, serverID)
	if err != nil {
		return "", err
	}
	if i == nil {
		return "", fmt.Errorf("VM %s not found", serverID)
	}
	for _, t := range i.Tags {
		if t == networkID {
			return i.PrivateIP, nil // attached already
		}
	}
	if err := api.AddTag(ctx, serverID, networkID); err != nil {
		return "", &cloud.OpError{Op: "adding the network tag of the NATS group", Write: true, Err: err}
	}
	return i.PrivateIP, nil
}

// DeletePrivateNetwork deletes the rule if viper made it for a group; ErrNetworkInUse if VMs of the group exist.
func (c *Client) DeletePrivateNetwork(ctx context.Context, networkID string) error {
	api, err := c.client(ctx)
	if err != nil {
		return err
	}
	r, err := api.NATSFirewall(ctx, networkID)
	if err != nil || r == nil {
		return err
	}
	group, _, _, ok := markerOf(r)
	if !ok || r.Name != RuleName(group) {
		return fmt.Errorf("the firewall rule %s was not made by viper for a NATS group: not deleting it", networkID)
	}
	members, err := api.ListInstances(ctx, map[string]string{model.LabelManaged: "true", model.LabelNATSGroup: group})
	if err != nil {
		return err
	}
	for _, m := range members {
		if m.Status != "STOPPING" { // a VM that is being deleted
			return cloud.ErrNetworkInUse
		}
	}
	return api.DeleteFirewall(ctx, networkID)
}

// ListGroupMachines returns the VMs of the group with their labels; a VM with labels that make no sense is an error.
func (c *Client) ListGroupMachines(ctx context.Context, group string) ([]model.GroupMember, error) {
	if err := model.CheckNATSName("NATS group", group); err != nil {
		return nil, err
	}
	api, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	all, err := api.ListInstances(ctx, map[string]string{model.LabelManaged: "true", model.LabelNATSGroup: group})
	if err != nil {
		return nil, err
	}
	var out []model.GroupMember
	for _, i := range all {
		if i.Status == "STOPPING" {
			continue
		}
		meta, ok, err := model.NATSMetadataFromLabels(i.Labels)
		if err != nil {
			return nil, fmt.Errorf("VM %s: %w", i.Name, err)
		}
		if !ok || meta.Group != group {
			continue
		}
		out = append(out, model.GroupMember{Server: *serverFrom(i), NATS: meta})
	}
	return out, nil
}

// UpdateNATSMetadata sets the NATS labels of the VM and keeps its other labels.
func (c *Client) UpdateNATSMetadata(ctx context.Context, serverID string, meta model.NATSMetadata) error {
	if err := model.CheckNATSName("NATS group", meta.Group); err != nil {
		return err
	}
	api, err := c.client(ctx)
	if err != nil {
		return err
	}
	if err := api.SetLabels(ctx, serverID, model.NATSLabels(meta)); err != nil {
		return &cloud.OpError{Op: "setting the NATS labels", Write: true, Err: err}
	}
	return nil
}

// NATSMetadataOf returns the NATS labels of the VM, nil if it has none.
func (c *Client) NATSMetadataOf(ctx context.Context, serverID string) (*model.NATSMetadata, error) {
	api, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	i, err := api.Instance(ctx, serverID)
	if err != nil {
		return nil, err
	}
	if i == nil {
		return nil, fmt.Errorf("VM %s not found", serverID)
	}
	meta, ok, err := model.NATSMetadataFromLabels(i.Labels)
	if err != nil || !ok {
		return nil, err
	}
	return &meta, nil
}
