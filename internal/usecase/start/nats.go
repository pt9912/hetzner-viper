package start

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/natsserver"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
	"github.com/pt9912/hetzner-viper/internal/usecase/natsgroup"
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
	role, code, err := r.natsRole(own, net, created, members)
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

// firstStartWindow is how long after the network was made a group without a server is taken for
// one whose first machine is still being made (the network is made first, the server later).
// After that it counts as a start that was interrupted.
const firstStartWindow = 15 * time.Minute

// natsRole is what the machine does in the group: its own role if it has one, the server if it
// made the network, a client if the group has a server or is about to get one. Anything else is
// refused, never guessed.
func (r *runner) natsRole(own *model.NATSMetadata, net model.PrivateNetwork, created bool, members []model.GroupMember) (model.NATSRole, int, error) {
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
	case !net.Created.IsZero() && r.d.Now().Sub(net.Created) < firstStartWindow:
		r.printf("The first machine of the NATS group %s is still being made.\n", group)
		return model.NATSClient, 0, nil
	default:
		msg = fmt.Sprintf("The NATS group %s has a network but no server (a first start was interrupted, or the server was deleted). Run viper up on the machine that was to be the server only if no other machine of the group exists; otherwise delete the group's machines (viper down) and then its network viper-nats-%s.", group, group)
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

// ops are the operations over ssh that viper up and viper down share.
func (r *runner) ops() natsgroup.Ops {
	return natsgroup.Ops{Run: r.d.Run, SSH: r.d.SSH, Out: r.out, Sleep: r.d.Sleep, Jitter: natsgroup.RandomJitter}
}

// remote runs a script on this machine; see natsgroup.Ops.On.
func (r *runner) remote(ctx context.Context, stdin, script string, args ...string) (string, error) {
	return r.ops().On(ctx, natsgroup.Self(r.c), false, stdin, script, args...)
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
	r.serverReady() // role and network are set now

	var code int
	var err error
	if st.role == model.NATSServer {
		code, err = r.natsServer(ctx, ip)
	} else {
		code, err = r.natsClient(ctx)
	}
	if code != 0 || err != nil {
		meta.State = model.NATSFailed
		_ = st.api.UpdateNATSMetadata(ctx, srv.ID, meta)
		return code, err
	}
	meta.State = model.NATSReady
	if err := st.api.UpdateNATSMetadata(ctx, srv.ID, meta); err != nil {
		return r.fail("Setting the NATS labels of %s: %s", c.ServerName, r.d.Cloud.Describe(err))
	}
	return 0, nil
}

// natsTools installs the nats tool on the machine and returns the release of nats-server for it.
func (r *runner) natsTools(ctx context.Context, withServer bool) (int, error) {
	arch, err := r.remote(ctx, "", `uname -m`)
	if err != nil {
		return r.fail("NATS: reading the architecture failed:\n%s", err)
	}
	arch = strings.TrimSpace(arch)
	if withServer {
		rel, err := natsserver.ReleaseFor(arch)
		if err != nil {
			return r.fail("%v", err)
		}
		if _, err := r.remote(ctx, "", natsInstallScript, rel.Version, rel.URL, rel.SHA256); err != nil {
			return r.fail("NATS: installing nats-server failed:\n%s", err)
		}
	}
	cli, err := natsserver.CLIReleaseFor(arch)
	if err != nil {
		return r.fail("%v", err)
	}
	if _, err := r.remote(ctx, "", natsCLIScript, cli.Version, cli.URL, cli.SHA256); err != nil {
		return r.fail("NATS: installing the nats tool failed:\n%s", err)
	}
	return 0, nil
}

// natsIdentity creates the machine's identity (or keeps the one it has) and returns its public key.
func (r *runner) natsIdentity(ctx context.Context) (string, error) {
	id, err := natsserver.NewIdentity()
	if err != nil {
		return "", err
	}
	out, err := r.remote(ctx, id.Seed+"\n"+id.PublicKey+"\n", natsIdentityScript, natsserver.Dir)
	if err != nil {
		return "", err
	}
	return lastNonEmpty(out), nil
}

// natsRegister enters the machine and its key in the configuration of the server and has the
// server read it (natsgroup.Ops.Update keeps the entries of the others).
func (r *runner) natsRegister(ctx context.Context, server natsgroup.Target, serverIP, pub string) error {
	c := r.c
	add := func(l []natsserver.Client) ([]natsserver.Client, error) {
		return natsserver.WithClient(l, natsserver.Client{Machine: c.Name, PublicKey: pub}), nil
	}
	return r.ops().Update(ctx, server, c.NATS.Group, natsserver.Endpoint{Host: serverIP, Port: c.NATS.Port}, add)
}

// natsFinish writes the connection file, then logs in once with the nats tool to see that it all
// works: TLS, the key, the address.
func (r *runner) natsFinish(ctx context.Context, serverIP string) error {
	c := r.c
	if _, err := r.remote(ctx, natsserver.ClientEnv(c.NATS.Group, c.Name, serverIP, c.NATS.Port), natsFileScript, natsserver.Dir, "connection.env", "644"); err != nil {
		return err
	}
	_, err := r.remote(ctx, "", natsConnectScript, natsserver.Dir)
	return err
}

// natsServer installs and starts the NATS server of the group on the machine.
func (r *runner) natsServer(ctx context.Context, ip string) (int, error) {
	c := r.c
	fail := func(what string, err error) (int, error) {
		return r.fail("NATS server: %s failed:\n%s", what, err)
	}
	if code, err := r.natsTools(ctx, true); code != 0 || err != nil {
		return code, err
	}
	if _, err := r.remote(ctx, "", natsCertScript, natsserver.Dir, ip, "viper-nats-"+c.NATS.Group); err != nil {
		return fail("creating the certificate", err)
	}
	pub, err := r.natsIdentity(ctx)
	if err != nil {
		return fail("storing the identity", err)
	}
	if err := r.natsRegister(ctx, natsgroup.Self(c), ip, pub); err != nil {
		return fail("writing the configuration", err)
	}
	if _, err := r.remote(ctx, natsserver.UnitFile(), natsUnitScript, strings.TrimSuffix(natsserver.Unit, ".service"), ip, fmt.Sprint(c.NATS.Port)); err != nil {
		return fail("starting the service", err)
	}
	if _, err := r.remote(ctx, "", natsCheckScript, ip, fmt.Sprint(c.NATS.Port)); err != nil {
		return fail("checking the server", err)
	}
	if err := r.natsFinish(ctx, ip); err != nil {
		return fail("logging in", err)
	}
	r.printf("NATS server of the group %s is ready at %s:%d.\n", c.NATS.Group, ip, c.NATS.Port)
	return 0, nil
}

// natsWaitForServer waits until the server of the group is ready and returns it. A server that
// failed ends the wait at once: the client does not take over its role.
func (r *runner) natsWaitForServer(ctx context.Context) (model.GroupMember, int, error) {
	group := r.c.NATS.Group
	wait := r.d.ServerWait
	if wait <= 0 {
		wait = 5 * time.Minute
	}
	for i := 0; i < int(wait/(5*time.Second)); i++ {
		if interrupted(ctx) {
			code, err := r.fail("Interrupted.")
			return model.GroupMember{}, code, err
		}
		members, err := r.nats.api.ListGroupMachines(ctx, group)
		if err != nil {
			code, err := r.fail("Listing the NATS group %s: %s", group, r.d.Cloud.Describe(err))
			return model.GroupMember{}, code, err
		}
		for _, m := range members {
			if m.NATS.Role != model.NATSServer {
				continue
			}
			switch {
			case m.NATS.State == model.NATSFailed:
				code, err := r.fail("The NATS server %s of the group %s failed to start. Run viper up on it again, or delete the group.", m.Server.Name, group)
				return model.GroupMember{}, code, err
			case m.NATS.State == model.NATSReady && m.Server.PrivateIP != "":
				return m, 0, nil
			}
		}
		if i == 0 {
			r.printf("Waiting for the NATS server of the group %s ...\n", group)
		}
		r.d.Sleep(5 * time.Second)
	}
	code, err := r.fail("The NATS server of the group %s did not become ready within %d minutes.", group, int(wait.Minutes()))
	return model.GroupMember{}, code, err
}

// natsClient makes the machine a client of the group's server: its tool and key, its entry in the
// server's configuration, the certificate it trusts, and a login to try it.
func (r *runner) natsClient(ctx context.Context) (int, error) {
	c := r.c
	fail := func(what string, err error) (int, error) {
		return r.fail("NATS client: %s failed:\n%s", what, err)
	}
	server, code, err := r.natsWaitForServer(ctx)
	if code != 0 || err != nil {
		return code, err
	}
	target, err := r.ops().Peer(c, server.Server)
	if err != nil {
		return fail("reaching the server", err)
	}
	if code, err := r.natsTools(ctx, false); code != 0 || err != nil {
		return code, err
	}
	pub, err := r.natsIdentity(ctx)
	if err != nil {
		return fail("storing the identity", err)
	}
	if err := r.natsRegister(ctx, target, server.Server.PrivateIP, pub); err != nil {
		return fail("registering at the server", err)
	}
	cert, err := r.ops().On(ctx, target, true, "", "cat \"$1\"", natsserver.CertFile)
	if err != nil || !strings.Contains(cert, "BEGIN CERTIFICATE") {
		return fail("reading the certificate of the server", fmt.Errorf("%v", err))
	}
	if _, err := r.remote(ctx, cert, natsFileScript, natsserver.Dir, "server.crt", "644"); err != nil {
		return fail("storing the certificate", err)
	}
	if err := r.natsFinish(ctx, server.Server.PrivateIP); err != nil {
		return fail("logging in", err)
	}
	r.printf("NATS client %s of the group %s is ready (server %s:%d).\n", c.Name, c.NATS.Group, server.Server.PrivateIP, c.NATS.Port)
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
