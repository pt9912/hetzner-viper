package stop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/natsserver"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
	"github.com/pt9912/hetzner-viper/internal/usecase/natsgroup"
)

// natsPlan is what deleting a machine of a NATS group means for the group. The labels of the
// machine decide, not the settings: a machine that has them is treated as a member of its group
// even if NATS_ENABLED was turned off since.
type natsPlan struct {
	api    cloud.NATSProvider
	meta   model.NATSMetadata
	c      *model.Machine
	d      Deps
	others []model.GroupMember // the other machines of the group
	out    io.Writer
	errOut io.Writer
}

func machineOf(c *model.Machine, serverName string) string {
	return strings.TrimPrefix(serverName, c.ServerPrefix+"-")
}

// planNATS reads the machine's group. It returns nil if the machine belongs to none. A server with
// other machines in its group is not deleted without --force, and then only after the group's
// name is typed: the group ends with it.
func planNATS(ctx context.Context, c *model.Machine, force bool, d Deps, srv *model.Server, out, errOut io.Writer) (*natsPlan, int, error) {
	var api cloud.NATSProvider
	if !cloud.AsNATS(d.Cloud, &api) {
		return nil, 0, nil
	}
	meta, err := api.NATSMetadataOf(ctx, srv.ID)
	if err != nil {
		fmt.Fprintf(errOut, "%s: the NATS labels of the server %s make no sense: %v\nNothing was changed.\n", d.Cloud.Name(), c.ServerName, err)
		return nil, 1, driving.ErrShown
	}
	if meta == nil {
		return nil, 0, nil
	}
	members, err := api.ListGroupMachines(ctx, meta.Group)
	if err != nil {
		fmt.Fprintf(errOut, "Listing the NATS group %s failed: %s\n", meta.Group, d.Cloud.Describe(err))
		return nil, 1, driving.ErrShown
	}
	p := &natsPlan{api: api, meta: *meta, c: c, d: d, out: out, errOut: errOut}
	for _, m := range members {
		if m.Server.ID != srv.ID {
			p.others = append(p.others, m)
		}
	}
	if meta.Role != model.NATSServer || len(p.others) == 0 {
		return p, 0, nil
	}
	names := make([]string, len(p.others))
	for i, m := range p.others {
		names[i] = machineOf(c, m.Server.Name)
	}
	fmt.Fprintf(errOut, "%s is the NATS server of the group %s, and the group has other machines: %s.\n", c.Name, meta.Group, strings.Join(names, ", "))
	if !force {
		fmt.Fprintf(errOut, "Delete them first (viper down <machine>), or end the group with viper down %s --force.\n", c.Name)
		return nil, 1, driving.ErrShown
	}
	fmt.Fprintln(errOut, "With --force the server is deleted anyway: the group ends and those machines lose their connection.")
	if p.d.Ask("Type the name of the group ("+meta.Group+") to end it: ") != meta.Group {
		fmt.Fprintln(out, "Cancelled")
		return nil, 1, nil
	}
	return p, 0, nil
}

// revoke takes a client's key out of the server's configuration before the machine is deleted, so
// no valid identity of a deleted machine stays behind. If that fails the machine is not deleted,
// unless --force says so. Without a server in the group there is nothing to revoke.
func (p *natsPlan) revoke(ctx context.Context, force bool) (int, error) {
	if p == nil || p.meta.Role != model.NATSClient {
		return 0, nil
	}
	var server *model.GroupMember
	for i, m := range p.others {
		if m.NATS.Role == model.NATSServer {
			server = &p.others[i]
		}
	}
	if server == nil {
		fmt.Fprintf(p.out, "The group %s has no server any more: there is no key to revoke.\n", p.meta.Group)
		return 0, nil
	}
	err := p.revokeAt(ctx, server.Server)
	if err == nil {
		return 0, nil
	}
	fmt.Fprintf(p.errOut, "Revoking the key of %s at the NATS server %s failed:\n%v\n", p.c.Name, server.Server.Name, err)
	if !force {
		fmt.Fprintf(p.errOut, "The machine was NOT deleted, so no valid key stays behind. Fix the server, or delete anyway with viper down %s --force.\n", p.c.Name)
		return 1, driving.ErrShown
	}
	fmt.Fprintf(p.errOut, "WARNING: --force deletes the machine anyway; its entry stays in the configuration of %s until the server is deleted.\n", server.Server.Name)
	return 0, nil
}

func (p *natsPlan) revokeAt(ctx context.Context, server model.Server) error {
	ops := natsgroup.Ops{Run: p.d.Run, SSH: p.d.SSH, Out: p.out}
	target, err := ops.Peer(p.c, server)
	if err != nil {
		return err
	}
	machine := p.c.Name
	drop := func(l []natsserver.Client) ([]natsserver.Client, error) {
		out := natsserver.WithoutClient(l, machine)
		if len(out) == len(l) {
			return nil, natsgroup.ErrNoChange // it was never registered
		}
		return out, nil
	}
	return ops.Update(ctx, target, p.meta.Group, natsserver.Endpoint{}, drop)
}

// after removes the group's network once its last machine is gone.
func (p *natsPlan) after(ctx context.Context) {
	if p == nil {
		return
	}
	if len(p.others) > 0 {
		fmt.Fprintf(p.out, "The private network of the group %s stays until its last machine is deleted.\n", p.meta.Group)
		return
	}
	net, err := p.api.FindPrivateNetwork(ctx, p.meta.Group)
	if err != nil || net == nil {
		if err != nil {
			fmt.Fprintf(p.errOut, "WARNING: looking for the private network of the group %s failed: %v\n", p.meta.Group, err)
		}
		return
	}
	if err := p.api.DeletePrivateNetwork(ctx, net.ID); err != nil {
		hint := ""
		if errors.Is(err, cloud.ErrNetworkInUse) {
			hint = " (a machine is still attached; try again in a moment)"
		}
		fmt.Fprintf(p.errOut, "WARNING: the private network %s was not removed%s: %v\n", net.Name, hint, err)
		return
	}
	fmt.Fprintf(p.out, "Private network %s removed (last machine of the group %s).\n", net.Name, p.meta.Group)
}
