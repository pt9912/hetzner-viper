package servertest

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/hetzner"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

var _ cloud.NATSProvider = (*Cloud)(nil)

// setLabels keeps a copy of the labels of a server (the caller holds the lock).
func (c *Cloud) setLabels(server string, labels map[string]string) {
	if c.Labels == nil {
		c.Labels = map[string]map[string]string{}
	}
	cp := make(map[string]string, len(labels))
	for k, v := range labels {
		cp[k] = v
	}
	c.Labels[server] = cp
}

// ListGroupMachines returns the servers of the NATS group, like the Hetzner adapter does: only
// servers marked as managed, with the group's name, and an error for labels that make no sense.
func (c *Cloud) ListGroupMachines(_ context.Context, group string) ([]model.GroupMember, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ListErr != nil {
		return nil, c.ListErr
	}
	var out []model.GroupMember
	for name, srv := range c.Servers {
		l := c.Labels[name]
		if l[model.LabelManaged] != "true" || l[model.LabelNATSGroup] != group {
			continue
		}
		meta, ok, err := model.NATSMetadataFromLabels(l)
		if err != nil {
			return nil, fmt.Errorf("server %s: %w", name, err)
		}
		if ok {
			out = append(out, model.GroupMember{Server: *srv, NATS: meta})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Server.Name < out[j].Server.Name })
	return out, nil
}

// UpdateNATSMetadata sets the NATS labels of a server and keeps its other labels.
func (c *Cloud) UpdateNATSMetadata(_ context.Context, serverID string, meta model.NATSMetadata) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, s := range c.Servers {
		if s.ID != serverID {
			continue
		}
		l := map[string]string{}
		for k, v := range c.Labels[name] {
			l[k] = v
		}
		for k, v := range model.NATSLabels(meta) {
			l[k] = v
		}
		c.setLabels(name, l)
		c.call("server labels %s role=%s state=%s", name, meta.Role, meta.State)
		return nil
	}
	return fmt.Errorf("server %s not found", serverID)
}

// zoneOf is the network zone of a location of the fake project, "" if there is none.
func zoneOf(location string) string {
	switch location {
	case "fsn1", "nbg1", "hel1":
		return "eu-central"
	case "ash":
		return "us-east"
	case "hil":
		return "us-west"
	case "sin":
		return "ap-southeast"
	}
	return ""
}

// NetworkZone is the network zone of a location.
func (*Cloud) NetworkZone(_ context.Context, location string) (string, error) {
	if z := zoneOf(location); z != "" {
		return z, nil
	}
	return "", fmt.Errorf("location %s not found", location)
}

// EnsurePrivateNetwork creates the network of the group; like the real API it refuses a second
// network of the same name, which is what makes exactly one caller the creator.
func (c *Cloud) EnsurePrivateNetwork(ctx context.Context, group, location string) (model.PrivateNetwork, bool, error) {
	zone, err := c.NetworkZone(ctx, location)
	if err != nil {
		return model.PrivateNetwork{}, false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ListErr != nil {
		return model.PrivateNetwork{}, false, c.ListErr
	}
	if c.Networks == nil {
		c.Networks = map[string]*model.PrivateNetwork{}
	}
	name := hetzner.NetworkName(group)
	if n, ok := c.Networks[name]; ok {
		if n.Zone != zone {
			return model.PrivateNetwork{}, false, fmt.Errorf("the network %s is in the zone %s, but %s is in %s", name, n.Zone, location, zone)
		}
		return *n, false, nil
	}
	c.nextID++
	n := &model.PrivateNetwork{ID: strconv.FormatInt(c.nextID, 10), Name: name, IPRange: fmt.Sprintf("10.%d.0.0/16", 100+len(c.Networks)), Zone: zone}
	c.Networks[name] = n
	c.call("network create %s", name)
	return *n, true, nil
}

// FindPrivateNetwork returns the network of the group, nil if there is none.
func (c *Cloud) FindPrivateNetwork(_ context.Context, group string) (*model.PrivateNetwork, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n, ok := c.Networks[hetzner.NetworkName(group)]; ok {
		cp := *n
		return &cp, nil
	}
	return nil, nil
}

// attachLocked attaches the server to the network (by ID) and returns its IP; the caller holds
// the lock. A server that is attached already keeps its IP.
func (c *Cloud) attachLocked(server, networkID string) string {
	if c.attached == nil {
		c.attached = map[string]string{}
	}
	var net *model.PrivateNetwork
	for _, n := range c.Networks {
		if n.ID == networkID {
			net = n
		}
	}
	if net == nil {
		return ""
	}
	if s := c.Servers[server]; s != nil && s.PrivateIP != "" && c.attached[server] == net.Name {
		return s.PrivateIP
	}
	count := 0
	for _, n := range c.attached {
		if n == net.Name {
			count++
		}
	}
	c.attached[server] = net.Name
	prefix := net.IPRange[:len(net.IPRange)-len("0.0/16")]
	return fmt.Sprintf("%s0.%d", prefix, 2+count)
}

// AttachPrivateNetwork attaches an existing server to the network.
func (c *Cloud) AttachPrivateNetwork(_ context.Context, serverID, networkID string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, s := range c.Servers {
		if s.ID != serverID {
			continue
		}
		ip := c.attachLocked(name, networkID)
		if ip == "" {
			return "", fmt.Errorf("network %s not found", networkID)
		}
		s.PrivateIP = ip
		c.call("network attach %s", name)
		return ip, nil
	}
	return "", fmt.Errorf("server %s not found", serverID)
}

// DeletePrivateNetwork deletes the network unless a server is attached.
func (c *Cloud) DeletePrivateNetwork(_ context.Context, networkID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, n := range c.Networks {
		if n.ID != networkID {
			continue
		}
		for _, att := range c.attached {
			if att == name {
				return cloud.ErrNetworkInUse
			}
		}
		delete(c.Networks, name)
		c.call("network delete %s", name)
		return nil
	}
	return nil
}

// NATSMetadataOf returns the NATS labels of a server, nil if it has none.
func (c *Cloud) NATSMetadataOf(_ context.Context, serverID string) (*model.NATSMetadata, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, s := range c.Servers {
		if s.ID != serverID {
			continue
		}
		meta, ok, err := model.NATSMetadataFromLabels(c.Labels[name])
		if err != nil || !ok {
			return nil, err
		}
		return &meta, nil
	}
	return nil, fmt.Errorf("server %s not found", serverID)
}
