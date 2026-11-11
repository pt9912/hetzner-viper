package check

import (
	"context"
	_ "embed"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pt9912/hetzner-viper/internal/mcpbin"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/natsserver"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

// natsStatus runs on the machine and prints key=value lines; it changes nothing there.
//
//go:embed natsstatus.sh
var natsStatus string

// natsChecks checks the NATS side of a machine that belongs to a NATS group: what the provider
// says (group, server, network, address), what the machine itself sees (certificate, login, the
// listening address of the server) and, from this computer, that the port is not open to the
// internet. Nothing is changed. A machine without NATS labels gets no section.
func natsChecks(ctx context.Context, r *report, c *model.Machine, d Deps, srv *model.Server) {
	var api cloud.NATSProvider
	if !cloud.AsNATS(d.Cloud, &api) {
		return
	}
	meta, err := api.NATSMetadataOf(ctx, srv.ID)
	if err != nil {
		fmt.Fprintf(r.out, "== NATS\n")
		r.bad("the NATS labels of %s make no sense: %v", c.ServerName, err)
		return
	}
	if meta == nil {
		if c.NATS.Enabled {
			fmt.Fprintf(r.out, "== NATS\n")
			r.warn("NATS_ENABLED is on, but %s is in no NATS group yet: run viper up %s", c.ServerName, c.Name)
		}
		return
	}
	fmt.Fprintf(r.out, "== NATS (group %s, %s)\n", meta.Group, meta.Role)
	members := natsCloudChecks(ctx, r, c, api, srv, meta)
	st := natsMachineChecks(ctx, r, c, d, srv, members)
	natsPortCheck(ctx, r, d, srv, st)
}

// natsCloudChecks are the checks that need nothing but the provider; it returns the group.
func natsCloudChecks(ctx context.Context, r *report, c *model.Machine, api cloud.NATSProvider, srv *model.Server, meta *model.NATSMetadata) []model.GroupMember {
	if c.NATS.Enabled && c.NATS.Group != meta.Group {
		r.bad("NATS_GROUP is %s, but %s belongs to the group %s (a machine is not moved to another group; delete it first)", c.NATS.Group, c.ServerName, meta.Group)
	} else {
		r.ok("group %s (labels of %s)", meta.Group, c.ServerName)
	}
	members, err := api.ListGroupMachines(ctx, meta.Group)
	if err != nil {
		r.bad("listing the group %s: %v", meta.Group, err)
		return nil
	}
	natsServerRoleChecks(r, meta.Group, members)
	if meta.State == model.NATSFailed {
		r.bad("this machine is marked failed: run viper up %s again", c.Name)
	}
	natsNetworkChecks(ctx, r, c, api, srv, meta.Group)
	return members
}

// natsServerRoleChecks: exactly one server in the group, and it is ready.
func natsServerRoleChecks(r *report, group string, members []model.GroupMember) {
	var servers []model.GroupMember
	for _, m := range members {
		if m.NATS.Role == model.NATSServer {
			servers = append(servers, m)
		}
	}
	switch {
	case len(servers) == 1:
		r.ok("exactly one server in the group: %s", servers[0].Server.Name)
	case len(servers) == 0:
		r.bad("the group %s has no server", group)
	default:
		r.bad("the group %s has %d servers, there must be exactly one", group, len(servers))
	}
	for _, s := range servers {
		switch s.NATS.State {
		case model.NATSReady:
			r.ok("server state of %s: ready", s.Server.Name)
		case model.NATSProvisioning:
			r.warn("server state of %s: provisioning (its viper up has not finished)", s.Server.Name)
		default:
			r.bad("server state of %s: %s (run viper up on it again)", s.Server.Name, s.NATS.State)
		}
	}
}

// natsNetworkChecks: the private network exists and the machine has an address in it.
func natsNetworkChecks(ctx context.Context, r *report, c *model.Machine, api cloud.NATSProvider, srv *model.Server, group string) {
	if net, err := api.FindPrivateNetwork(ctx, group); err != nil {
		r.bad("the private network: %v", err)
	} else if net == nil {
		r.bad("the private network viper-nats-%s does not exist (the group cannot talk; NATS is not moved to a public port)", group)
	} else {
		r.ok("private network %s (%s)", net.Name, net.Detail())
	}
	if !srv.InNetwork() {
		r.bad("%s has no address in the private network", c.ServerName)
	} else {
		r.ok("private address %s", srv.PrivateIP)
	}
}

// natsState is what the status script of the machine said.
type natsState struct {
	port string
	ok   bool
}

// natsMachineChecks look at the machine itself over ssh: the certificate it trusts, the login with
// its key and, on the server, how it listens and who is registered.
func natsMachineChecks(ctx context.Context, r *report, c *model.Machine, d Deps, srv *model.Server, members []model.GroupMember) natsState {
	stdout, _, err := d.Run(ctx, natsStatus, "ssh", "-F", c.SSHConfig, c.SSHAlias, "bash -s -- "+model.QuoteAll(natsserver.Dir))
	if err != nil {
		r.bad("SSH to %s for the NATS checks failed", c.SSHAlias)
		return natsState{}
	}
	v := parseProbe(stdout)
	if v["env"] != "ok" {
		r.bad("%s/connection.env is missing: the machine never finished joining the group (viper up %s)", natsserver.Dir, c.Name)
		return natsState{}
	}
	url := v["url"]
	host, port := splitURL(url)
	st := natsState{port: port, ok: true}
	natsCertChecks(r, v, members, host)
	switch v["login"] {
	case "ok":
		r.ok("logged in to %s over TLS with the NKey of the machine", url)
	case "no-tool":
		r.warn("the nats tool is not installed on the machine: no login test")
	default:
		r.bad("the login to %s failed: %s", url, v["login_err"])
	}
	if v["server"] == "yes" {
		natsServerChecks(r, c, v, members, srv)
	}
	natsMCPChecks(r, c, v, members)
	return st
}

// natsMCPChecks look at the MCP server of the machine (NATS_MCP): that it is there, that Claude Code
// knows it, and that the push list and the allowed tools are the ones of the settings.
func natsMCPChecks(r *report, c *model.Machine, v map[string]string, members []model.GroupMember) {
	if !c.NATS.MCP {
		if v["mcp_bin"] == "ok" {
			r.warn("the MCP server is installed on the machine, but NATS_MCP is off (viper up does not remove it)")
		}
		return
	}
	if v["mcp_bin"] != "ok" {
		r.bad("NATS_MCP is on, but %s/viper-nats-mcp is missing: run viper up %s", natsserver.Dir, c.Name)
		return
	}
	if !strings.HasPrefix(v["mcp_version"], "viper-nats-mcp") {
		r.bad("the MCP server on the machine does not run (wrong architecture?): %s; run viper up %s", v["mcp_version"], c.Name)
		return
	}
	r.ok("MCP server installed (%s)", v["mcp_version"])
	natsMCPVersionCheck(r, c, v)
	natsMCPPushMembers(r, c, members)
	switch v["mcp_claude"] {
	case "no-jq":
		r.warn("jq is missing on the machine: the entry of the MCP server in Claude Code was not checked")
	case "none", "":
		r.bad("Claude Code does not know the MCP server nats: run viper up %s", c.Name)
	case natsserver.Dir + "/viper-nats-mcp":
		r.ok("the MCP server is registered with Claude Code (%s)", v["mcp_claude"])
	default:
		r.bad("Claude Code starts %s for the MCP server nats, not %s/viper-nats-mcp: run viper up %s", v["mcp_claude"], natsserver.Dir, c.Name)
	}
	if want := strings.Join(c.NATS.MCPPushFrom, ","); v["mcp_push"] != want {
		r.warn("the machines whose messages are pushed are %q on the machine, NATS_MCP_PUSH_FROM says %q: run viper up %s", v["mcp_push"], want, c.Name)
	} else if want == "" {
		r.ok("no message is pushed into a session (NATS_MCP_PUSH_FROM is empty)")
	} else {
		r.ok("messages of %s are pushed into a session with the channel flag", want)
	}
	if want := strings.Join(c.NATS.MCPTopics, ","); v["mcp_topics"] != want {
		r.warn("the topics the MCP server subscribes to are %q on the machine, NATS_MCP_TOPICS says %q: run viper up %s", v["mcp_topics"], want, c.Name)
	}
	if want := strings.Join(model.MCPAllowedTools(c.NATS.MCPAutoAllow), ","); v["mcp_claude"] != "no-jq" && v["mcp_allowed"] != want {
		r.warn("the tools of the MCP server allowed without asking are %q, NATS_MCP_AUTOALLOW=%s says %q: run viper up %s", v["mcp_allowed"], c.NATS.MCPAutoAllow, want, c.Name)
	}
}

func splitURL(url string) (host, port string) {
	rest := strings.TrimPrefix(url, "tls://")
	h, p, err := net.SplitHostPort(rest)
	if err != nil {
		return rest, ""
	}
	return h, p
}

// natsCertChecks look at the certificate the machine trusts: not expired, not close to it, and
// for the address of the server.
func natsCertChecks(r *report, v map[string]string, members []model.GroupMember, host string) {
	natsCertDays(r, v["cert_days"])
	for _, m := range members {
		if m.NATS.Role == model.NATSServer && m.Server.PrivateIP != "" && v["cert_ip"] != "" && v["cert_ip"] != m.Server.PrivateIP {
			r.bad("the certificate is for %s, but the server is at %s now (its address changed: create the group again)", v["cert_ip"], m.Server.PrivateIP)
			return
		}
	}
	if v["cert_ip"] != "" && host != "" && v["cert_ip"] != host {
		r.bad("the certificate is for %s, but connection.env points to %s", v["cert_ip"], host)
	}
}

func natsCertDays(r *report, days string) {
	switch days {
	case "missing":
		r.bad("the server certificate is missing on the machine")
		return
	case "unreadable":
		r.bad("the server certificate cannot be read")
		return
	}
	n, err := strconv.Atoi(days)
	switch {
	case err != nil:
		r.bad("the server certificate: %q", days)
	case n < 0:
		r.bad("the server certificate has expired: the group is no longer usable, create it again")
	case n < 7:
		r.warn("the server certificate expires in %d days (it is not renewed: create the group again before)", n)
	default:
		r.ok("server certificate valid for %d more days", n)
	}
}

// natsServerChecks are the checks of the machine that runs the server.
func natsServerChecks(r *report, c *model.Machine, v map[string]string, members []model.GroupMember, srv *model.Server) {
	if v["unit"] == "active" {
		r.ok("service viper-nats is active")
	} else {
		r.bad("service viper-nats is %s", v["unit"])
	}
	if n := v["allow_responses"]; n != "" && n != "0" {
		r.warn("server.conf still has allow_responses (written by viper v0.15.0): any machine of the group can publish to any subject; run viper up on a machine of the group to write the configuration again")
	}
	natsListenCheck(r, v["listen"], srv.PrivateIP)
	natsRegistrationChecks(r, c, v["registered"], members)
}

// natsListenCheck: the server listens on the private address and on nothing else.
func natsListenCheck(r *report, listen, privateIP string) {
	switch {
	case listen == "":
		r.bad("nothing listens on the NATS port")
	case strings.Contains(listen, "0.0.0.0") || strings.Contains(listen, "*") || strings.Contains(listen, "[::]"):
		r.bad("the server listens on all addresses (%s), not only on the private one", listen)
	case privateIP != "" && !strings.HasPrefix(listen, privateIP+":"):
		r.bad("the server listens on %s, not on its private address %s", listen, privateIP)
	default:
		r.ok("the server listens only on %s", listen)
	}
}

// natsRegistrationChecks compares who is registered at the server with the machines of the group.
func natsRegistrationChecks(r *report, c *model.Machine, list string, members []model.GroupMember) {
	registered := map[string]bool{}
	for _, n := range strings.Split(list, ",") {
		if n != "" {
			registered[n] = true
		}
	}
	inGroup := map[string]bool{}
	var missing, orphans []string
	for _, m := range members {
		name := strings.TrimPrefix(m.Server.Name, c.ServerPrefix+"-")
		inGroup[name] = true
		if m.NATS.Role == model.NATSClient && m.NATS.State == model.NATSReady && !registered[name] {
			missing = append(missing, name)
		}
	}
	for n := range registered {
		if !inGroup[n] {
			orphans = append(orphans, n)
		}
	}
	sort.Strings(orphans)
	sort.Strings(missing)
	if len(orphans) > 0 {
		r.warn("registered at the server, but no machine of the group any more: %s (left by viper down --force; they go when the group ends)", strings.Join(orphans, ", "))
	}
	if len(missing) > 0 {
		r.bad("ready machines that are not registered at the server: %s (run viper up on them)", strings.Join(missing, ", "))
	}
	if len(orphans) == 0 && len(missing) == 0 {
		r.ok("%d machine(s) registered at the server, all of them in the group", len(registered))
	}
}

// natsPortCheck tries the NATS port on the public address of the machine from this computer: it
// must not answer.
func natsPortCheck(ctx context.Context, r *report, d Deps, srv *model.Server, st natsState) {
	if srv.IPv4 == "" || st.port == "" {
		return
	}
	addr := net.JoinHostPort(srv.IPv4, st.port)
	dial := d.Dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: 4 * time.Second}).DialContext
	}
	conn, err := dial(ctx, "tcp", addr)
	if err != nil {
		r.ok("%s is not reachable from the internet", addr)
		return
	}
	_ = conn.Close() // only opened to see that it answers
	r.bad("%s answers from the internet: the NATS port must be private only", addr)
}

// natsMCPVersionCheck compares the binary on the machine with the one this viper would install: after an
// update of viper the machines still run the old server until viper up is run for them again.
func natsMCPVersionCheck(r *report, c *model.Machine, v map[string]string) {
	var want []byte
	if f := c.NATS.MCPBinary; f != "" {
		want, _ = os.ReadFile(f)
	} else if rel, err := natsserver.ReleaseFor(v["mcp_arch"]); err == nil {
		want, _ = mcpbin.Binary(rel.Arch)
	}
	switch {
	case len(want) == 0 || v["mcp_sha"] == "":
		// this viper has no binary to compare with (a build without make): nothing to say
	case mcpbin.Checksum(want) != v["mcp_sha"]:
		r.warn("the MCP server on the machine is not the one of this viper (another version): run viper up %s to install it", c.Name)
	default:
		r.ok("the MCP server on the machine is the one of this viper")
	}
}

// natsMCPPushMembers warns about a machine in NATS_MCP_PUSH_FROM that is not in the group: nothing it sends
// can ever arrive from there, so the entry is a typo or a machine that is gone.
func natsMCPPushMembers(r *report, c *model.Machine, members []model.GroupMember) {
	if len(c.NATS.MCPPushFrom) == 0 || len(members) == 0 {
		return
	}
	in := map[string]bool{}
	for _, m := range members {
		in[strings.TrimPrefix(m.Server.Name, c.ServerPrefix+"-")] = true
	}
	for _, p := range c.NATS.MCPPushFrom {
		if !in[p] {
			r.warn("NATS_MCP_PUSH_FROM names %s, which is no machine of the group %s", p, c.NATS.Group)
		}
	}
}
