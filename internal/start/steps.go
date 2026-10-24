package start

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	hetznerviper "github.com/pt9912/hetzner-viper"
	"github.com/pt9912/hetzner-viper/internal/hetzner"
	"github.com/pt9912/hetzner-viper/internal/machine"
	"github.com/pt9912/hetzner-viper/internal/preflight"
)

// loginKey makes sure the login key is in the project and returns the name it has there.
// Hetzner allows each public key only once per project: reuse it under whatever name it has.
func (r *runner) loginKey(ctx context.Context) (string, int, error) {
	c := r.c
	fp := preflight.FingerprintMD5(c.SSHKey + ".pub")
	if fp == "" {
		code, err := r.fail("cannot read the public key %s.pub", c.SSHKey)
		return "", code, err
	}
	keys, err := r.d.Hetzner.SSHKeys(ctx)
	if err != nil {
		code, err := r.fail("Hetzner: listing the SSH keys failed (check HCLOUD_TOKEN / project / network)")
		return "", code, err
	}
	for _, k := range keys {
		if strings.EqualFold(k.Fingerprint, fp) {
			return k.Name, 0, nil
		}
	}
	for _, k := range keys {
		if k.Name == c.KeyName {
			code, err := r.fail("SSH key %s in Hetzner belongs to a different public key than %s.pub", c.KeyName, c.SSHKey)
			return "", code, err
		}
	}
	pub, err := os.ReadFile(c.SSHKey + ".pub")
	if err != nil {
		return "", 1, err
	}
	if err := r.d.Hetzner.SSHKeyCreate(ctx, c.KeyName, string(pub)); err != nil {
		code, err := r.fail("uploading the SSH key %s failed: %s", c.KeyName, hetzner.Error(err))
		return "", code, err
	}
	r.printf("SSH key %s uploaded.\n", c.KeyName)
	return c.KeyName, 0, nil
}

// firewall makes sure the shared firewall exists and has the rules for SSH (TCP 22) and ICMP
// (ping, for diagnosis) from SSH_ALLOWED_CIDRS. A missing rule is added (also to an existing
// firewall); other rules are left alone.
func (r *runner) firewall(ctx context.Context) (int, error) {
	c := r.c
	rules, exists, err := r.d.Hetzner.FirewallRules(ctx, c.FirewallName)
	if err != nil {
		return r.fail("Hetzner: listing the firewalls failed (check HCLOUD_TOKEN / project / network): %s", hetzner.Error(err))
	}
	if !exists {
		if err := r.d.Hetzner.FirewallCreate(ctx, c.FirewallName); err != nil {
			return r.fail("creating the firewall %s failed: %s", c.FirewallName, hetzner.Error(err))
		}
		r.printf("Firewall %s created.\n", c.FirewallName)
		rules = nil
	}
	has := func(proto, port string) bool {
		for _, rule := range rules {
			if rule.Direction == "in" && rule.Protocol == proto && (port == "" || rule.Port == port) {
				return true
			}
		}
		return false
	}
	cidrs := strings.Split(c.Setting("SSH_ALLOWED_CIDRS", "0.0.0.0/0,::/0"), ",")
	hadBoth := has("tcp", "22") && has("icmp", "")
	if !has("tcp", "22") {
		if err := r.d.Hetzner.FirewallAddRule(ctx, c.FirewallName, hetzner.FirewallRule{Direction: "in", Protocol: "tcp", Port: "22", SourceIPs: cidrs}); err != nil {
			return r.fail("adding the SSH rule to firewall %s failed: %s", c.FirewallName, hetzner.Error(err))
		}
	}
	if !has("icmp", "") {
		if err := r.d.Hetzner.FirewallAddRule(ctx, c.FirewallName, hetzner.FirewallRule{Direction: "in", Protocol: "icmp", SourceIPs: cidrs}); err != nil {
			return r.fail("adding the ICMP rule to firewall %s failed: %s", c.FirewallName, hetzner.Error(err))
		}
	}
	if hadBoth {
		r.printf("Existing firewall %s: verify that its rules permit your connection.\n", c.FirewallName)
	}
	return 0, nil
}

// server creates the server, with a host key signed by the host CA, or reuses the existing one.
// reused is true if it counts as set up by a completed run.
func (r *runner) server(ctx context.Context, ca, keyName, ipadKeyFile string) (srv *hetzner.Server, reused bool, code int, err error) {
	c := r.c
	existing, err := r.d.Hetzner.ServerByName(ctx, c.ServerName)
	if err != nil {
		code, err := r.fail("Hetzner: listing the servers failed (check HCLOUD_TOKEN / project / network): %s", hetzner.Error(err))
		return nil, false, code, err
	}
	if existing != nil {
		r.printf("Existing server: reusing it.\n")
		id := fmtID(existing.ID)
		machine.ForgetStalePin(c, id, r.out)
		r.lg.Event("reusing server id=%s", id)
		// "reused" means set up by a completed run; a server whose first run was interrupted may
		// still be booting (transient ssh errors, the reboot after its updates still to come)
		reused = readTrim(c.ReadyState) == id
		// a server older than 30 minutes is long past its first boot (also for another computer
		// without the local marker): permanent ssh errors then end the wait at once
		if !existing.Created.IsZero() && r.d.Now().Sub(existing.Created) > 30*time.Minute {
			reused = true
		}
		return r.ipOrFail(existing, reused)
	}

	login, err := os.ReadFile(c.SSHKey + ".pub")
	if err != nil {
		return nil, false, 1, err
	}
	keys := []string{string(login)}
	if ipadKeyFile != "" {
		b, err := os.ReadFile(ipadKeyFile)
		if err != nil {
			return nil, false, 1, err
		}
		keys = append(keys, string(b))
	}
	hk, err := MakeHostKey(r.d.Keygen, ca, c.ServerName, c.DNSHost, r.d.Now())
	if err != nil {
		code, err := r.fail("creating the host key failed: %v", err)
		return nil, false, code, err
	}
	userData, err := UserData(hetznerviper.CloudInit, keys, hk.Private, hk.Public, hk.Certificate)
	if err != nil {
		return nil, false, 1, err
	}
	if err := os.WriteFile(c.KnownPin, []byte(hk.KnownHostsLine+"\n"), 0o600); err != nil {
		return nil, false, 1, err
	}
	_ = os.Chmod(c.KnownPin, 0o600)
	created, err := r.d.Hetzner.ServerCreate(ctx, hetzner.ServerSpec{
		Name: c.ServerName, Type: c.ServerType, Location: c.ServerLocation, Image: c.ServerImage,
		SSHKey: keyName, Firewall: c.FirewallName, UserData: userData,
	})
	if err != nil {
		code, err := r.fail("creating the server %s failed: %s", c.ServerName, hetzner.Error(err))
		return nil, false, code, err
	}
	r.printf("Server %s created.\n", c.ServerName)
	id := fmtID(created.ID)
	_ = os.WriteFile(c.IDState, []byte(id+"\n"), 0o600)
	r.lg.Event("server created id=%s", id)
	// a new server has none of the workspaces an earlier one with this name had
	_ = os.Remove(c.WorkspaceState) // best effort: may not exist
	_ = os.Remove(c.ReadyState)
	return r.ipOrFail(created, false)
}

func (r *runner) ipOrFail(s *hetzner.Server, reused bool) (*hetzner.Server, bool, int, error) {
	if s.IPv4 == "" {
		code, err := r.fail("server %s has no IPv4 address", r.c.ServerName)
		return nil, false, code, err
	}
	return s, reused, 0, nil
}

func fmtID(id int64) string { return strconv.FormatInt(id, 10) }

// The probe always exits 0 once connected; only connection failures are retried.
// cloud-init exits 0 (done), 2 (done with warnings) or 1 (errors).
// reboot=pending: updates were installed and cloud-init is about to restart the server.
const probe = `cloud-init status --wait >/dev/null 2>&1; echo "cloud-init=$?"; test -f /var/tmp/claude-ready && echo claude=ok; docker info >/dev/null 2>&1 && echo docker=ok; test -f "${VIPER_REBOOT_FILE:-/var/run/reboot-required}" && echo reboot=pending; echo "ready=$(cat ~/.config/viper/ready 2>/dev/null)"; true`

func hasLine(s, line string) bool {
	for _, l := range strings.Split(s, "\n") {
		if l == line {
			return true
		}
	}
	return false
}

func lineValue(s, key string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, key+"=") {
			return strings.TrimPrefix(l, key+"=")
		}
	}
	return ""
}

// rebootWait counts the waits for the reboot that follows a new server's updates.
type rebootWait struct {
	waits     int
	announced bool
}

// wait reports whether to keep waiting for the reboot (at most 36 times), saying so once.
func (w *rebootWait) wait(r *runner) bool {
	w.waits++
	if w.waits > 36 {
		return false
	}
	if !w.announced {
		r.printf("Updates need a reboot; waiting for the server to restart ...\n")
		w.announced = true
	}
	return true
}

// reportSSHFailure explains why the wait for ssh ended without success.
func (r *runner) reportSSHFailure(errMsg string) {
	c := r.c
	switch machine.SSHFailureKind(errMsg) {
	case "hostkey":
		r.eprintf("SSH: the server's host key is not trusted; server retained.\n")
		r.eprintf("  %s\n", errMsg)
		r.eprintf("  The server answers but does not prove to be %s (stale pin, expired certificate without pin, or someone in between).\n", c.ServerName)
	case "auth":
		r.eprintf("SSH: the server rejects the login key %s (%s); server retained.\n", c.SSHKey, errMsg)
		r.eprintf("  Was the server created with another login key? Its authorized key is set at creation.\n")
	default:
		last := ""
		if errMsg != "" {
			last = " Last error: " + errMsg
		}
		r.eprintf("SSH not reachable; server retained.%s\n", last)
	}
}

// waitForServer waits until ssh works and cloud-init is done. While a new server boots, sshd may
// briefly present a key other than the provisioned one and builder may not exist yet, so host-key
// and auth errors are retried too. A reused server is already up: those errors are permanent
// there and end the wait at once.
func (r *runner) waitForServer(ctx context.Context, id string, reused bool) (string, int, error) {
	c := r.c
	r.printf("Waiting for SSH and cloud-init ...\n")
	status, errMsg := "", ""
	var rw rebootWait
	for i := 1; i <= 120; i++ {
		if ctx.Err() != nil {
			return "", 1, ErrShown
		}
		stdout, stderr, err := r.d.Run(ctx, "", "ssh", "-n", "-F", c.SSHConfig, "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", c.SSHAlias, probe)
		if err == nil {
			status = stdout
			// set up by a completed run (from this or another computer): the server records it itself
			if lineValue(status, "ready") == id {
				reused = true
			}
			// A new server restarts once after its updates: wait for it instead of working into it.
			// The post-upgrade reboot follows cloud-init within a minute or so; after ~3 minutes the
			// flag is from something else (e.g. later unattended upgrades): carry on with a warning.
			if !reused && hasLine(status, "reboot=pending") && rw.wait(r) {
				status, errMsg = "", "the server did not come back after the reboot for its updates"
				r.d.Sleep(5 * time.Second)
				continue
			}
			break
		}
		status, errMsg = "", strings.TrimRight(stderr, "\n")
		if reused && machine.SSHFailureKind(errMsg) != "network" {
			break
		}
		r.d.Sleep(5 * time.Second)
	}
	if status != "" {
		return status, 0, nil
	}
	r.reportSSHFailure(errMsg)
	return "", 1, ErrShown
}
