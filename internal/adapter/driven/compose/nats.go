package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

// NATS groups on a Docker host (docs/plan/compose.md §5). The "private network" of a group is a Docker network
// viper-nats-<group> that viper makes itself with docker network create: Docker refuses a second network of a name, so
// only one caller becomes the server candidate (the lock of the first start). The compose files include it as an
// external network, and a machine of a group is on that network only: a container that is not on it cannot reach the
// port. The server is found on it by the name of the group (an alias), which its certificate is for.
//
// Group and role are labels of the container and never change. The state (provisioning, ready, failed) changes, and
// Docker cannot change the labels of a container: it is kept in small labelled volumes
// viper-nats-<group>_<machine>_<state>, a change makes the new one and removes the old (docs/plan/compose.md §5, D7).

var _ cloud.NATSProvider = (*Client)(nil)

const natsPrefix = "viper-nats-"

// NetworkName is the name of the Docker network of a NATS group.
func NetworkName(group string) string { return natsPrefix + group }

func stateVolume(group, machine string, state model.NATSState) string {
	return natsPrefix + group + "_" + machine + "_" + string(state) // _ cannot be in the names: two groups never share a name
}

// NetworkZone is the Docker host: the machines of a group must be on one host (one network).
func (*Client) NetworkZone(_ context.Context, location string) (string, error) {
	if location == "" {
		return "", fmt.Errorf("no Docker host: set SERVER_LOCATION")
	}
	return location, nil
}

type networkInfo struct {
	ID      string            `json:"Id"`
	Name    string            `json:"Name"`
	Labels  map[string]string `json:"Labels"`
	IPAM    struct{ Config []struct{ Subnet string } }
	Created string `json:"Created"`
}

func (n networkInfo) subnet() string {
	for _, c := range n.IPAM.Config {
		if !strings.Contains(c.Subnet, ":") {
			return c.Subnet
		}
	}
	return ""
}

// network returns the Docker network of that name, nil if there is none; ErrNotOurs if it is not the group's.
func (c *Client) network(ctx context.Context, group string) (*networkInfo, error) {
	out, err := c.docker(ctx, "network", "inspect", "--", NetworkName(group))
	if notFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var list []networkInfo
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list) != 1 {
		return nil, fmt.Errorf("docker network inspect %s: unexpected answer", NetworkName(group))
	}
	n := &list[0]
	if n.Labels[labelManaged] != "true" || n.Labels[model.LabelNATSGroup] != group {
		return nil, fmt.Errorf("%w: network %s (for the NATS group %s)", ErrNotOurs, n.Name, group)
	}
	return n, nil
}

func (c *Client) networkModel(n *networkInfo) model.PrivateNetwork {
	return model.PrivateNetwork{ID: n.Name, Name: n.Name, IPRange: n.subnet(), Zone: c.opts.Context}
}

// subnetOf is the subnet of the group network a machine is on ("" without one).
func (c *Client) subnetOf(ctx context.Context, networkID string) (string, error) {
	if networkID == "" {
		return "", nil
	}
	group := strings.TrimPrefix(networkID, natsPrefix)
	n, err := c.network(ctx, group)
	if err != nil {
		return "", err
	}
	if n == nil || n.subnet() == "" {
		return "", fmt.Errorf("the network %s of the NATS group is not there or has no IPv4 subnet", networkID)
	}
	return n.subnet(), nil
}

// EnsurePrivateNetwork makes the network of the group, or finds it; see cloud.NATSProvider.
func (c *Client) EnsurePrivateNetwork(ctx context.Context, group, location string) (model.PrivateNetwork, bool, error) {
	if err := model.CheckNATSName("NATS group", group); err != nil {
		return model.PrivateNetwork{}, false, err
	}
	if _, err := c.NetworkZone(ctx, location); err != nil {
		return model.PrivateNetwork{}, false, err
	}
	_, err := c.docker(ctx, "network", "create", "--label", labelManaged+"=true", "--label", model.LabelNATSGroup+"="+group, "--", NetworkName(group))
	created := err == nil
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return model.PrivateNetwork{}, false, &cloud.OpError{Op: "creating the network " + NetworkName(group), Write: true, Err: err}
	}
	n, err := c.network(ctx, group) // somebody else was first: use the network, if it is the group's
	if err != nil {
		return model.PrivateNetwork{}, false, err
	}
	if n == nil {
		return model.PrivateNetwork{}, false, fmt.Errorf("the network %s is gone again", NetworkName(group))
	}
	return c.networkModel(n), created, nil
}

// FindPrivateNetwork returns the network of the group, nil if there is none (or if the name is another's).
func (c *Client) FindPrivateNetwork(ctx context.Context, group string) (*model.PrivateNetwork, error) {
	if err := model.CheckNATSName("NATS group", group); err != nil {
		return nil, err
	}
	n, err := c.network(ctx, group)
	if err != nil {
		if strings.Contains(err.Error(), ErrNotOurs.Error()) {
			return nil, nil
		}
		return nil, err
	}
	if n == nil {
		return nil, nil
	}
	m := c.networkModel(n)
	return &m, nil
}

// AttachPrivateNetwork cannot be done for a machine that exists: its labels (group and role) cannot be changed, and it is
// on the network of its own project. The machine has to be made again.
func (*Client) AttachPrivateNetwork(context.Context, string, string) (string, error) {
	return "", fmt.Errorf("a machine made without the NATS group cannot join it later with Docker Compose: viper down it and viper up it again")
}

// DeletePrivateNetwork removes the network if viper made it; ErrNetworkInUse if machines are still on it.
func (c *Client) DeletePrivateNetwork(ctx context.Context, networkID string) error {
	group := strings.TrimPrefix(networkID, natsPrefix)
	n, err := c.network(ctx, group)
	if err != nil || n == nil {
		return err
	}
	members, err := c.ListGroupMachines(ctx, group)
	if err != nil {
		return err
	}
	if len(members) > 0 {
		return cloud.ErrNetworkInUse
	}
	if _, err := c.docker(ctx, "network", "rm", "--", NetworkName(group)); err != nil && !notFound(err) {
		if strings.Contains(err.Error(), "active endpoints") {
			return cloud.ErrNetworkInUse
		}
		return err
	}
	return nil
}

// stateVolumes are the names of the NATS state volumes of the machine.
func (c *Client) stateVolumes(ctx context.Context, machine string) ([]string, error) {
	out, err := c.docker(ctx, "volume", "ls", "--quiet", "--filter", "label="+labelManaged+"=true", "--filter", "label="+labelServer+"="+machine, "--filter", "label="+model.LabelNATSState)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// stateOf is the state the volumes of the machine say, "" if there is none. Several can be left by a change that was cut
// off after the new one was made: a later state wins over provisioning.
func (c *Client) stateOf(ctx context.Context, machine string) (model.NATSState, error) {
	out, err := c.docker(ctx, "volume", "ls", "--filter", "label="+labelManaged+"=true", "--filter", "label="+labelServer+"="+machine,
		"--filter", "label="+model.LabelNATSState, "--format", `{{.Label "`+model.LabelNATSState+`"}}`)
	if err != nil {
		return "", err
	}
	var best model.NATSState
	for _, f := range strings.Fields(out) {
		switch st := model.NATSState(f); {
		case best == "" || best == model.NATSProvisioning:
			best = st
		case st == model.NATSFailed:
			best = st
		}
	}
	return best, nil
}

// metadataOf reads the NATS labels of a container, with the state from the volumes if a change has been made.
func (c *Client) metadataOf(ctx context.Context, in *inspected) (model.NATSMetadata, bool, error) {
	labels := map[string]string{}
	for k, v := range in.Config.Labels {
		labels[k] = v
	}
	if st, err := c.stateOf(ctx, in.Config.Labels[labelServer]); err != nil {
		return model.NATSMetadata{}, false, err
	} else if st != "" && labels[model.LabelNATSGroup] != "" {
		labels[model.LabelNATSState] = string(st)
	}
	return model.NATSMetadataFromLabels(labels)
}

// ListGroupMachines returns the machines of the group with their labels; one with labels that make no sense is an error.
func (c *Client) ListGroupMachines(ctx context.Context, group string) ([]model.GroupMember, error) {
	if err := model.CheckNATSName("NATS group", group); err != nil {
		return nil, err
	}
	ids, err := c.docker(ctx, "container", "ls", "--all", "--quiet", "--no-trunc", "--filter", "label="+labelManaged+"=true", "--filter", "label="+model.LabelNATSGroup+"="+group)
	if err != nil {
		return nil, err
	}
	var out []model.GroupMember
	for _, id := range strings.Fields(ids) {
		in, err := c.container(ctx, id)
		if err != nil {
			return nil, err
		}
		if in == nil {
			continue
		}
		name := in.Config.Labels[labelServer]
		meta, ok, err := c.metadataOf(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("container %s: %w", name, err)
		}
		if !ok || meta.Group != group {
			continue
		}
		out = append(out, model.GroupMember{Server: *serverOf(name, in), NATS: meta})
	}
	return out, nil
}

// UpdateNATSMetadata records the state of the machine: the new volume is made first, then the old ones go, so that a cut
// off change leaves a state behind and never none. Only the machine itself writes its state, so no lock is needed.
func (c *Client) UpdateNATSMetadata(ctx context.Context, serverID string, meta model.NATSMetadata) error {
	if err := model.CheckNATSName("NATS group", meta.Group); err != nil {
		return err
	}
	in, err := c.container(ctx, serverID)
	if err != nil {
		return err
	}
	if in == nil {
		return fmt.Errorf("server %s not found", serverID)
	}
	name := in.Config.Labels[labelServer]
	if _, err := c.docker(ctx, "volume", "create",
		"--label", labelManaged+"=true", "--label", labelServer+"="+name,
		"--label", model.LabelNATSGroup+"="+meta.Group, "--label", model.LabelNATSRole+"="+string(meta.Role), "--label", model.LabelNATSState+"="+string(meta.State),
		"--", stateVolume(meta.Group, name, meta.State)); err != nil {
		return &cloud.OpError{Op: "recording the NATS state", Write: true, Err: err}
	}
	old, err := c.stateVolumes(ctx, name)
	if err != nil {
		return err
	}
	for _, v := range old {
		if v == stateVolume(meta.Group, name, meta.State) {
			continue
		}
		if _, err := c.docker(ctx, "volume", "rm", "--", v); err != nil && !notFound(err) {
			return &cloud.OpError{Op: "removing the old NATS state " + v, Write: true, Err: err}
		}
	}
	return nil
}

// NATSMetadataOf returns the NATS labels of the machine, nil if it has none.
func (c *Client) NATSMetadataOf(ctx context.Context, serverID string) (*model.NATSMetadata, error) {
	in, err := c.container(ctx, serverID)
	if err != nil {
		return nil, err
	}
	if in == nil {
		return nil, fmt.Errorf("server %s not found", serverID)
	}
	meta, ok, err := c.metadataOf(ctx, in)
	if err != nil || !ok {
		return nil, err
	}
	return &meta, nil
}
