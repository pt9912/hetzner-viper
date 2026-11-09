package start

import (
	"context"
	"fmt"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/natsserver"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

// natsState is what viper up knows of the machine's NATS group between the steps.
type natsState struct {
	api     cloud.NATSProvider
	network model.PrivateNetwork
	role    model.NATSRole
	// createdNetwork is whether this run created the network: if the server then cannot be created,
	// the network is not left behind.
	createdNetwork bool
}

// natsLabels are the labels a new server gets (nil without NATS).
func (r *runner) natsLabels() map[string]string {
	if r.nats == nil {
		return nil
	}
	return model.NATSLabels(model.NATSMetadata{Group: r.c.NATS.Group, Role: r.nats.role, State: model.NATSProvisioning})
}

// natsNetwork is the ID of the network a new server is attached to when it is created.
func (r *runner) natsNetwork() string {
	if r.nats == nil {
		return ""
	}
	return r.nats.network.ID
}

// natsPrepare decides the role of the machine before anything of it is created: it makes sure
// of the group's network (the create is the lock of the first start) and, if somebody else made
// it, finds the server of the group.
func (r *runner) natsPrepare(ctx context.Context) (int, error) {
	c := r.c
	var api cloud.NATSProvider
	if !cloud.AsNATS(r.d.Cloud, &api) {
		return r.fail("NATS groups are not supported with %s", r.d.Cloud.Name())
	}
	group := c.NATS.Group
	st := &natsState{api: api}
	own, code, err := r.natsOwnMembership(ctx, api)
	if code != 0 || err != nil {
		return code, err
	}
	net, created, err := api.EnsurePrivateNetwork(ctx, group, c.ServerLocation)
	if err != nil {
		return r.fail("The private network of the NATS group %s: %s", group, r.d.Cloud.Describe(err))
	}
	st.network, st.createdNetwork = net, created
	if created {
		r.printf("Private network %s (%s) created for the NATS group %s.\n", net.Name, net.IPRange, group)
	}
	members, err := api.ListGroupMachines(ctx, group)
	if err != nil {
		return r.fail("Listing the NATS group %s: %s", group, r.d.Cloud.Describe(err))
	}
	role, code, err := r.natsRole(own, created, members)
	if code != 0 || err != nil {
		return code, err
	}
	st.role = role
	r.nats = st
	r.lg.Event("nats group=%s role=%s network=%s created=%v", group, st.role, net.ID, created)
	r.printf("NATS group %s: this machine is the %s.\n", group, st.role)
	return 0, nil
}

// natsOwnMembership reads the NATS labels of the machine if it exists. A machine keeps its group:
// it is never moved to another one without being asked.
func (r *runner) natsOwnMembership(ctx context.Context, api cloud.NATSProvider) (*model.NATSMetadata, int, error) {
	c := r.c
	existing, err := r.d.Cloud.ServerByName(ctx, c.ServerName)
	if err != nil {
		code, err := r.opFail(&cloud.OpError{Op: "listing the servers", Err: err})
		return nil, code, err
	}
	if existing == nil {
		return nil, 0, nil
	}
	own, err := api.NATSMetadataOf(ctx, existing.ID)
	if err != nil {
		code, err := r.fail("%s: the NATS labels of the server %s make no sense: %v", r.d.Cloud.Name(), c.ServerName, err)
		return nil, code, err
	}
	if own != nil && own.Group != c.NATS.Group {
		code, err := r.fail("The server %s belongs to the NATS group %s, not %s (NATS_GROUP). Delete it first (viper down %s) to move it to another group.", c.ServerName, own.Group, c.NATS.Group, c.Name)
		return nil, code, err
	}
	return own, 0, nil
}

// natsRole is what the machine does in the group: its own role if it has one, the server if it
// made the network, a client if the group has a server. Anything else is refused, never guessed.
func (r *runner) natsRole(own *model.NATSMetadata, created bool, members []model.GroupMember) (model.NATSRole, int, error) {
	group := r.c.NATS.Group
	servers := 0
	for _, m := range members {
		if m.NATS.Role == model.NATSServer {
			servers++
		}
	}
	var msg string
	switch {
	case servers > 1:
		msg = fmt.Sprintf("The NATS group %s has %d servers; there must be exactly one. Nothing was changed.", group, servers)
	case own != nil:
		return own.Role, 0, nil
	case created && len(members) == 0:
		return model.NATSServer, 0, nil
	case created:
		msg = fmt.Sprintf("The network of the NATS group %s was just created, but the group has machines. Nothing was changed.", group)
	case servers == 1:
		return model.NATSClient, 0, nil
	default:
		msg = fmt.Sprintf("The NATS group %s has a network but no server (a first start was interrupted?). Run viper up on the machine that was to be the server, or delete the group's machines (viper down) and its network viper-nats-%s.", group, group)
	}
	code, err := r.fail("%s", msg)
	return "", code, err
}

// natsCleanup removes the network this run created if no machine came of it.
func (r *runner) natsCleanup(ctx context.Context) {
	if r.nats == nil || !r.nats.createdNetwork {
		return
	}
	if members, err := r.nats.api.ListGroupMachines(ctx, r.c.NATS.Group); err != nil || len(members) > 0 {
		return
	}
	if err := r.nats.api.DeletePrivateNetwork(ctx, r.nats.network.ID); err != nil {
		r.warnf(ctx, "The private network %s was left behind: %v\n", r.nats.network.Name, err)
		return
	}
	r.printf("Private network %s removed again.\n", r.nats.network.Name)
}

// remote runs a script on the server with data on stdin and returns what it printed. Unlike
// script, the script is the argument of bash -c, because stdin carries the data.
func (r *runner) remote(ctx context.Context, stdin, script string, args ...string) (string, error) {
	cmd := "bash -c " + model.Quote(script) + " nats " + model.QuoteAll(args...)
	sshArgs := []string{"-F", r.c.SSHConfig, "-o", "BatchMode=yes", r.c.SSHAlias, cmd}
	if stdin == "" {
		sshArgs = append([]string{"-n"}, sshArgs...)
	}
	stdout, stderr, err := r.d.Run(ctx, stdin, "ssh", sshArgs...)
	for _, l := range strings.Split(strings.TrimRight(stdout, "\n"), "\n") {
		if strings.HasPrefix(l, "nats") {
			r.printf("%s\n", l)
		}
	}
	if err != nil {
		return stdout, fmt.Errorf("%s", strings.TrimSpace(lastLines(stdout, 6)+"\n"+stderr))
	}
	return stdout, nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// natsSetup makes the machine what its role says, once it can be reached by ssh.
func (r *runner) natsSetup(ctx context.Context, srv *model.Server) (int, error) {
	c, st := r.c, r.nats
	meta := model.NATSMetadata{Group: c.NATS.Group, Role: st.role, State: model.NATSProvisioning}

	// a machine that exists without NATS joins now: network and labels
	ip := srv.PrivateIP
	if ip == "" {
		var err error
		if ip, err = st.api.AttachPrivateNetwork(ctx, srv.ID, st.network.ID); err != nil {
			return r.fail("Attaching %s to the private network: %s", c.ServerName, r.d.Cloud.Describe(err))
		}
	}
	if err := st.api.UpdateNATSMetadata(ctx, srv.ID, meta); err != nil {
		return r.fail("Setting the NATS labels of %s: %s", c.ServerName, r.d.Cloud.Describe(err))
	}
	r.printf("Private address of %s: %s\n", c.ServerName, ip)

	if st.role != model.NATSServer {
		meta.State = model.NATSFailed
		_ = st.api.UpdateNATSMetadata(ctx, srv.ID, meta)
		return r.fail("Joining a NATS group as a client is not implemented yet; this machine was attached to the network only.")
	}
	if code, err := r.natsServer(ctx, ip); code != 0 || err != nil {
		meta.State = model.NATSFailed
		_ = st.api.UpdateNATSMetadata(ctx, srv.ID, meta)
		return code, err
	}
	meta.State = model.NATSReady
	if err := st.api.UpdateNATSMetadata(ctx, srv.ID, meta); err != nil {
		return r.fail("Setting the NATS labels of %s: %s", c.ServerName, r.d.Cloud.Describe(err))
	}
	r.printf("NATS server of the group %s is ready at %s:%d.\n", c.NATS.Group, ip, c.NATS.Port)
	return 0, nil
}

// natsServer installs and starts the NATS server of the group on the machine.
func (r *runner) natsServer(ctx context.Context, ip string) (int, error) {
	c := r.c
	fail := func(what string, err error) (int, error) {
		return r.fail("NATS server: %s failed:\n%s", what, err)
	}
	arch, _, err := r.d.Run(ctx, "", "ssh", "-n", "-F", c.SSHConfig, "-o", "BatchMode=yes", c.SSHAlias, "uname -m")
	if err != nil {
		return fail("reading the architecture", err)
	}
	rel, err := natsserver.ReleaseFor(strings.TrimSpace(arch))
	if err != nil {
		return r.fail("%v", err)
	}
	if _, err := r.remote(ctx, "", natsInstallScript, rel.Version, rel.URL, rel.SHA256); err != nil {
		return fail("installing nats-server", err)
	}
	if _, err := r.remote(ctx, "", natsCertScript, natsserver.Dir, ip, "viper-nats-"+c.NATS.Group); err != nil {
		return fail("creating the certificate", err)
	}
	id, err := natsserver.NewIdentity()
	if err != nil {
		return fail("creating the identity", err)
	}
	out, err := r.remote(ctx, id.Seed+"\n"+id.PublicKey+"\n", natsIdentityScript, natsserver.Dir)
	if err != nil {
		return fail("storing the identity", err)
	}
	pub := lastNonEmpty(out)
	cfg, err := natsserver.Config{Group: c.NATS.Group, Host: ip, Port: c.NATS.Port, Clients: []natsserver.Client{{Machine: c.Name, PublicKey: pub}}}.Render()
	if err != nil {
		return fail("building the configuration", err)
	}
	if _, err := r.remote(ctx, cfg, natsConfigScript, natsserver.Dir); err != nil {
		return fail("writing the configuration", err)
	}
	if _, err := r.remote(ctx, natsserver.UnitFile(), natsUnitScript, strings.TrimSuffix(natsserver.Unit, ".service"), ip, fmt.Sprint(c.NATS.Port)); err != nil {
		return fail("starting the service", err)
	}
	if _, err := r.remote(ctx, "", natsCheckScript, ip, fmt.Sprint(c.NATS.Port)); err != nil {
		return fail("checking the server", err)
	}
	return 0, nil
}

func lastNonEmpty(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}

// ensureServerInGroup is ensureServer, with the NATS group's network and role decided first and
// the network removed again if the server could not be created.
func (r *runner) ensureServerInGroup(ctx context.Context, ca, ipadKey string) (*model.Server, bool, int, error) {
	if r.c.NATS.Enabled {
		if code, err := r.natsPrepare(ctx); code != 0 || err != nil {
			return nil, false, code, err
		}
	}
	srv, reused, code, err := r.ensureServer(ctx, ca, ipadKey)
	if code != 0 || err != nil {
		r.natsCleanup(ctx)
	}
	return srv, reused, code, err
}

// natsJoin makes the machine a member of its NATS group; without NATS it does nothing.
func (r *runner) natsJoin(ctx context.Context, srv *model.Server) (int, error) {
	if r.nats == nil {
		return 0, nil
	}
	return r.natsSetup(ctx, srv)
}
