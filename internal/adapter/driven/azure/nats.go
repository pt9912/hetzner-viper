package azure

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

// NATS groups on Azure (docs/plan/nats-anbieter.md §3.4). The "private network" of a group is the application
// security group viper-nats-<group> of the resource group: the network interfaces of the VMs of the group are
// in it, and the network security group of each VM has the rule viper-nats-<group> that lets the NATS port in
// from that application security group to itself. Making the application security group is the lock of the
// first start: the PUT asks for If-None-Match: *, so a second one is refused (412). THAT IS NOT PROVEN
// against a real subscription; if Azure ignored the header, two simultaneous first starts would both become
// servers, and viper check would report "the group has 2 servers". The virtual network is the one of the
// VMs; nothing in it is changed. The NATS labels are tags of the VM.

var _ cloud.NATSProvider = (*Client)(nil)

const natsPrefix = "viper-nats-"

// GroupName is the name of the application security group of a NATS group.
func GroupName(group string) string { return natsPrefix + group }

// inGroup tells whether the network interface of the VM is in an application security group of a NATS group.
func inGroup(v *VM) bool {
	for _, g := range v.InGroups {
		if strings.HasPrefix(g, natsPrefix) {
			return true
		}
	}
	return false
}

// NetworkZone is the location: the interfaces of a group must be in the region of its application security group.
func (*Client) NetworkZone(_ context.Context, location string) (string, error) {
	if location == "" {
		return "", errors.New("no location: set SERVER_LOCATION")
	}
	return strings.ToLower(location), nil
}

func ours(a *GroupASG, group string) bool {
	return a.Tags[model.LabelManaged] == "true" && a.Tags[model.LabelNATSGroup] == group
}

func networkOf(a *GroupASG) *model.PrivateNetwork {
	return &model.PrivateNetwork{ID: a.Name, Name: a.Name, Zone: strings.ToLower(a.Location), Created: a.Created}
}

// EnsurePrivateNetwork makes the application security group of the group, or finds it; see cloud.NATSProvider.
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
	name := GroupName(group)
	tags := map[string]string{model.LabelManaged: "true", model.LabelNATSGroup: group}
	err = api.CreateGroupASG(ctx, name, tags)
	switch {
	case err == nil:
		a, err := api.GroupASG(ctx, name)
		if err != nil || a == nil {
			return model.PrivateNetwork{}, false, fmt.Errorf("the application security group %s was made but cannot be read: %v", name, err)
		}
		return *networkOf(a), true, nil
	case !errors.Is(err, ErrExists):
		return model.PrivateNetwork{}, false, &cloud.OpError{Op: fmt.Sprintf("making the application security group %s", name), Write: true, Err: err}
	}
	have, err := api.GroupASG(ctx, name)
	if err != nil {
		return model.PrivateNetwork{}, false, err
	}
	if have == nil {
		return model.PrivateNetwork{}, false, fmt.Errorf("the application security group %s was refused as existing but cannot be found: try again", name)
	}
	if !ours(have, group) {
		return model.PrivateNetwork{}, false, fmt.Errorf("an application security group named %s exists that viper did not make for the group %s", name, group)
	}
	if !strings.EqualFold(have.Location, zone) {
		return model.PrivateNetwork{}, false, fmt.Errorf("the NATS group %s exists in %s; this machine goes to %s: all machines of a group must be in one region", group, have.Location, zone)
	}
	return *networkOf(have), false, nil
}

// FindPrivateNetwork returns the application security group of the group, nil if there is none.
func (c *Client) FindPrivateNetwork(ctx context.Context, group string) (*model.PrivateNetwork, error) {
	if err := model.CheckNATSName("NATS group", group); err != nil {
		return nil, err
	}
	api, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	a, err := api.GroupASG(ctx, GroupName(group))
	if err != nil || a == nil || !ours(a, group) {
		return nil, err
	}
	return networkOf(a), nil
}

// AttachPrivateNetwork puts the interface of the VM into the group and returns its private address.
func (c *Client) AttachPrivateNetwork(ctx context.Context, serverID, networkID string) (string, error) {
	api, err := c.client(ctx)
	if err != nil {
		return "", err
	}
	v, err := api.VM(ctx, serverID)
	if err != nil {
		return "", err
	}
	if v == nil {
		return "", fmt.Errorf("VM %s not found", serverID)
	}
	asg, err := api.GroupASG(ctx, networkID)
	if err != nil {
		return "", err
	}
	if asg == nil {
		return "", fmt.Errorf("the application security group %s does not exist", networkID)
	}
	if err := api.JoinGroup(ctx, serverID, strings.TrimPrefix(networkID, natsPrefix), asg.ID, c.opts.natsPort()); err != nil {
		return "", &cloud.OpError{Op: "putting the network interface into the NATS group", Write: true, Err: err}
	}
	return v.PrivateIP, nil
}

// DeletePrivateNetwork removes the rule of the group and deletes the application security group if viper made
// it; ErrNetworkInUse if VMs of the group exist.
func (c *Client) DeletePrivateNetwork(ctx context.Context, networkID string) error {
	api, err := c.client(ctx)
	if err != nil {
		return err
	}
	a, err := api.GroupASG(ctx, networkID)
	if err != nil || a == nil {
		return err
	}
	group := a.Tags[model.LabelNATSGroup]
	if a.Name != GroupName(group) || a.Tags[model.LabelManaged] != "true" {
		return fmt.Errorf("the application security group %s was not made by viper for a NATS group: not deleting it", networkID)
	}
	vms, err := api.ListVMs(ctx, map[string]string{model.LabelManaged: "true", model.LabelNATSGroup: group})
	if err != nil {
		return err
	}
	if len(vms) > 0 {
		return cloud.ErrNetworkInUse
	}
	if err := api.DropGroupRule(ctx, group); err != nil {
		return err
	}
	if err := api.DeleteGroupASG(ctx, networkID); errors.Is(err, ErrInUse) {
		return cloud.ErrNetworkInUse
	} else if err != nil {
		return err
	}
	return nil
}

// ListGroupMachines returns the VMs of the group with their tags; a VM with tags that make no sense is an error.
func (c *Client) ListGroupMachines(ctx context.Context, group string) ([]model.GroupMember, error) {
	if err := model.CheckNATSName("NATS group", group); err != nil {
		return nil, err
	}
	api, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	vms, err := api.ListVMs(ctx, map[string]string{model.LabelManaged: "true", model.LabelNATSGroup: group})
	if err != nil {
		return nil, err
	}
	var out []model.GroupMember
	for _, v := range vms {
		meta, ok, err := model.NATSMetadataFromLabels(v.Tags)
		if err != nil {
			return nil, fmt.Errorf("VM %s: %w", v.Name, err)
		}
		if !ok || meta.Group != group {
			continue
		}
		out = append(out, model.GroupMember{Server: *serverFrom(v), NATS: meta})
	}
	return out, nil
}

// UpdateNATSMetadata sets the NATS tags of the VM and keeps its other tags.
func (c *Client) UpdateNATSMetadata(ctx context.Context, serverID string, meta model.NATSMetadata) error {
	if err := model.CheckNATSName("NATS group", meta.Group); err != nil {
		return err
	}
	api, err := c.client(ctx)
	if err != nil {
		return err
	}
	if err := api.SetVMTags(ctx, serverID, model.NATSLabels(meta)); err != nil {
		return &cloud.OpError{Op: "setting the NATS tags", Write: true, Err: err}
	}
	return nil
}

// NATSMetadataOf returns the NATS tags of the VM, nil if it has none.
func (c *Client) NATSMetadataOf(ctx context.Context, serverID string) (*model.NATSMetadata, error) {
	api, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	v, err := api.VM(ctx, serverID)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, fmt.Errorf("VM %s not found", serverID)
	}
	meta, ok, err := model.NATSMetadataFromLabels(v.Tags)
	if err != nil || !ok {
		return nil, err
	}
	return &meta, nil
}
