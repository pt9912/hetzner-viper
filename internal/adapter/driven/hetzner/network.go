package hetzner

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"strconv"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

// NetworkName is the name of the private network of a NATS group. It is the same for every caller
// on purpose: Hetzner allows a network name once per project, so creating the network is the lock
// that makes exactly one of several first starts the server candidate.
func NetworkName(group string) string { return "viper-nats-" + group }

// NetworkZone is the network zone of a location.
func (h *Client) NetworkZone(ctx context.Context, location string) (string, error) {
	l, _, err := h.c.Location.GetByName(ctx, location)
	if err != nil {
		return "", err
	}
	if l == nil {
		return "", fmt.Errorf("location %s not found", location)
	}
	return string(l.NetworkZone), nil
}

// PickRange chooses the address range for a new group network: a /16 in 10.100.0.0 to 10.199.0.0
// that overlaps none of the existing ranges, and the /24 subnet in it. The search starts at a
// place that depends on the group, so groups started at the same time rarely choose the same
// range (a clash is harmless: Hetzner lets networks overlap; it only matters if they are connected).
func PickRange(existing []*net.IPNet, group string) (network, subnet string, err error) {
	f := fnv.New32a()
	_, _ = f.Write([]byte(group))
	start := int(f.Sum32() % 100)
	for i := 0; i < 100; i++ {
		second := 100 + (start+i)%100
		_, cand, _ := net.ParseCIDR(fmt.Sprintf("10.%d.0.0/16", second))
		free := true
		for _, e := range existing {
			if e != nil && (e.Contains(cand.IP) || cand.Contains(e.IP)) {
				free = false
				break
			}
		}
		if free {
			return cand.String(), fmt.Sprintf("10.%d.0.0/24", second), nil
		}
	}
	return "", "", errors.New("no free address range left in 10.100.0.0/16 to 10.199.0.0/16: delete networks that are not needed")
}

func networkFrom(n *hcloud.Network) *model.PrivateNetwork {
	out := &model.PrivateNetwork{ID: strconv.FormatInt(n.ID, 10), Name: n.Name, Created: n.Created}
	if n.IPRange != nil {
		out.IPRange = n.IPRange.String()
	}
	if len(n.Subnets) > 0 {
		out.Zone = string(n.Subnets[0].NetworkZone)
	}
	return out
}

// ours tells whether viper created the network for the group.
func ours(n *hcloud.Network, group string) bool {
	return n.Labels[model.LabelManaged] == "true" && n.Labels[model.LabelNATSGroup] == group
}

// EnsurePrivateNetwork creates the private network of the group or finds it; see cloud.NATSProvider.
func (h *Client) EnsurePrivateNetwork(ctx context.Context, group, location string) (model.PrivateNetwork, bool, error) {
	if err := model.CheckNATSName("NATS group", group); err != nil {
		return model.PrivateNetwork{}, false, err
	}
	zone, err := h.NetworkZone(ctx, location)
	if err != nil {
		return model.PrivateNetwork{}, false, err
	}
	all, err := h.c.Network.All(ctx)
	if err != nil {
		return model.PrivateNetwork{}, false, err
	}
	taken := make([]*net.IPNet, 0, len(all))
	for _, n := range all {
		taken = append(taken, n.IPRange)
	}
	rng, sub, err := PickRange(taken, group)
	if err != nil {
		return model.PrivateNetwork{}, false, err
	}
	_, ipRange, _ := net.ParseCIDR(rng)
	_, subRange, _ := net.ParseCIDR(sub)
	name := NetworkName(group)
	n, _, err := h.c.Network.Create(ctx, hcloud.NetworkCreateOpts{
		Name: name, IPRange: ipRange,
		Subnets: []hcloud.NetworkSubnet{{Type: hcloud.NetworkSubnetTypeCloud, IPRange: subRange, NetworkZone: hcloud.NetworkZone(zone)}},
		Labels:  map[string]string{model.LabelManaged: "true", model.LabelNATSGroup: group},
	})
	if err == nil {
		return *networkFrom(n), true, nil
	}
	if !hcloud.IsError(err, hcloud.ErrorCodeUniquenessError) {
		return model.PrivateNetwork{}, false, err
	}
	// somebody was first: use the network, if it is the group's and in the zone
	n, _, err = h.c.Network.GetByName(ctx, name)
	if err != nil {
		return model.PrivateNetwork{}, false, err
	}
	if n == nil {
		return model.PrivateNetwork{}, false, fmt.Errorf("the network %s was refused as existing but cannot be found: try again", name)
	}
	if !ours(n, group) {
		return model.PrivateNetwork{}, false, fmt.Errorf("a network named %s exists that viper did not create for the group %s", name, group)
	}
	got := networkFrom(n)
	if got.Zone != zone {
		return model.PrivateNetwork{}, false, fmt.Errorf("the network %s is in the zone %s, but %s is in %s: all machines of a NATS group must be in one network zone", name, got.Zone, location, zone)
	}
	return *got, false, nil
}

// FindPrivateNetwork returns the private network of the group, nil if there is none.
func (h *Client) FindPrivateNetwork(ctx context.Context, group string) (*model.PrivateNetwork, error) {
	if err := model.CheckNATSName("NATS group", group); err != nil {
		return nil, err
	}
	name := NetworkName(group)
	n, _, err := h.c.Network.GetByName(ctx, name)
	if err != nil || n == nil {
		return nil, err
	}
	if !ours(n, group) {
		return nil, fmt.Errorf("a network named %s exists that viper did not create for the group %s", name, group)
	}
	return networkFrom(n), nil
}

func privateIPIn(s *hcloud.Server, networkID int64) string {
	for _, p := range s.PrivateNet {
		if p.Network != nil && p.Network.ID == networkID && p.IP != nil {
			return p.IP.String()
		}
	}
	return ""
}

// AttachPrivateNetwork attaches the server to the network and returns its private IP.
func (h *Client) AttachPrivateNetwork(ctx context.Context, serverID, networkID string) (string, error) {
	sid, err := strconv.ParseInt(serverID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("not a server ID: %s", serverID)
	}
	nid, err := strconv.ParseInt(networkID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("not a network ID: %s", networkID)
	}
	s, _, err := h.c.Server.GetByID(ctx, sid)
	if err != nil {
		return "", err
	}
	if s == nil {
		return "", fmt.Errorf("server %s not found", serverID)
	}
	if ip := privateIPIn(s, nid); ip != "" {
		return ip, nil // attached already
	}
	act, _, err := h.c.Server.AttachToNetwork(ctx, s, hcloud.ServerAttachToNetworkOpts{Network: &hcloud.Network{ID: nid}})
	if err != nil {
		return "", err
	}
	if err := h.c.Action.WaitFor(ctx, act); err != nil {
		return "", err
	}
	s, _, err = h.c.Server.GetByID(ctx, sid)
	if err != nil {
		return "", err
	}
	if s == nil {
		return "", fmt.Errorf("server %s not found", serverID)
	}
	ip := privateIPIn(s, nid)
	if ip == "" {
		return "", fmt.Errorf("server %s has no private IP in the network after it was attached", serverID)
	}
	return ip, nil
}

// DeletePrivateNetwork deletes the network if no server is attached to it.
func (h *Client) DeletePrivateNetwork(ctx context.Context, networkID string) error {
	nid, err := strconv.ParseInt(networkID, 10, 64)
	if err != nil {
		return fmt.Errorf("not a network ID: %s", networkID)
	}
	n, _, err := h.c.Network.GetByID(ctx, nid)
	if err != nil {
		return err
	}
	if n == nil {
		return nil
	}
	if len(n.Servers) > 0 || len(n.LoadBalancers) > 0 {
		return cloud.ErrNetworkInUse
	}
	_, err = h.c.Network.Delete(ctx, n)
	return err
}
