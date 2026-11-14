package start

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	hetznerviper "github.com/pt9912/hetzner-viper"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

// loginKey makes sure the login key is in the provider and returns the reference to it.
func (r *runner) loginKey(ctx context.Context) (string, int, error) {
	c := r.c
	pub, err := os.ReadFile(c.SSHKey + ".pub")
	if err != nil || model.FingerprintMD5(string(pub)) == "" {
		code, err := r.fail("cannot read the public key %s.pub", c.SSHKey)
		return "", code, err
	}
	key, err := r.d.Cloud.EnsureLoginKey(ctx, c.KeyName, string(pub))
	var conflict *cloud.KeyConflictError
	switch {
	case errors.As(err, &conflict):
		code, err := r.fail("SSH key %s in %s belongs to a different public key than %s.pub", c.KeyName, r.d.Cloud.Name(), c.SSHKey)
		return "", code, err
	case err != nil:
		code, err := r.opFail(err)
		return "", code, err
	}
	if key.Created {
		r.printf("SSH key %s uploaded.\n", c.KeyName)
	}
	return key.Ref, 0, nil
}

// opFail shows the failure of a step of the provider: a read that failed points to the access
// (credentials, project, network), a write says what could not be done.
func (r *runner) opFail(err error) (int, error) {
	cl := r.d.Cloud
	var op *cloud.OpError
	if errors.As(err, &op) {
		if op.Write {
			return r.fail("%s failed: %s", op.Op, cl.Describe(op.Err))
		}
		return r.fail("%s: %s failed (check %s): %s", cl.Name(), op.Op, cl.Hint(), cl.Describe(op.Err))
	}
	return r.fail("%s: %s", cl.Name(), cl.Describe(err))
}

// firewall makes sure the shared firewall exists and has the rules for SSH (TCP 22) and ICMP
// (ping, for diagnosis) from SSH_ALLOWED_CIDRS.
func (r *runner) firewall(ctx context.Context) (int, error) {
	c := r.c
	if c.IsContainer() {
		return 0, nil // a pod is reached through the API of the cluster: there is no port to open
	}
	cidrs := strings.Split(c.Setting("SSH_ALLOWED_CIDRS", "0.0.0.0/0,::/0"), ",")
	res, err := r.d.Cloud.EnsureFirewall(ctx, c.FirewallName, cidrs)
	if res.Created {
		r.printf("Firewall %s created.\n", c.FirewallName)
	}
	if err != nil {
		return r.opFail(err)
	}
	if res.Complete {
		r.printf("Existing firewall %s: verify that its rules permit your connection.\n", c.FirewallName)
	}
	return 0, nil
}

// prepareImage lets a provider that has to bring the image to where the machines run do it before the first one is made.
func (r *runner) prepareImage(ctx context.Context) (int, error) {
	var prep cloud.ImagePreparer
	if !cloud.As(r.d.Cloud, &prep) {
		return 0, nil
	}
	if err := prep.PrepareImage(ctx, r.out); err != nil {
		return r.opFail(&cloud.OpError{Op: "preparing the image", Write: true, Err: err})
	}
	return 0, nil
}

// startExisting asks a provider that can start a machine to start the existing one (a container that was stopped), and
// reads it again: a machine that was stopped has no address in its network until it runs.
func (r *runner) startExisting(ctx context.Context, existing *model.Server) (*model.Server, int, error) {
	var st cloud.Starter
	if !cloud.As(r.d.Cloud, &st) {
		return existing, 0, nil
	}
	if err := st.EnsureRunning(ctx, r.c.ServerName); err != nil {
		code, err := r.opFail(&cloud.OpError{Op: "starting the machine", Write: true, Err: err})
		return nil, code, err
	}
	if again, err := r.d.Cloud.ServerByName(ctx, r.c.ServerName); err == nil && again != nil {
		return again, 0, nil
	}
	return existing, 0, nil
}

// reuse takes the server that exists: starts it if it is a stopped container and decides whether it counts as set up.
func (r *runner) reuse(ctx context.Context, existing *model.Server) (srv *model.Server, reused bool, code int, err error) {
	c := r.c
	r.printf("Existing server: reusing it.\n")
	r.existed = true
	id := existing.ID
	r.d.SSH.ForgetStalePin(c, id, r.out)
	existing, code, err = r.startExisting(ctx, existing)
	if err != nil || code != 0 {
		return nil, false, code, err
	}
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

// server creates the server, with a host key signed by the host CA, or reuses the existing one.
// reused is true if it counts as set up by a completed run.
func (r *runner) server(ctx context.Context, ca, keyName, ipadKeyFile string) (srv *model.Server, reused bool, code int, err error) {
	c := r.c
	existing, err := r.d.Cloud.ServerByName(ctx, c.ServerName)
	if err != nil {
		code, err := r.opFail(&cloud.OpError{Op: "listing the servers", Err: err})
		return nil, false, code, err
	}
	if existing != nil {
		return r.reuse(ctx, existing)
	}

	if code, err := r.prepareImage(ctx); err != nil || code != 0 {
		return nil, false, code, err
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
	created, err := r.d.Cloud.CreateServer(ctx, model.ServerSpec{
		Name: c.ServerName, Size: c.ServerType, Region: c.ServerLocation, Image: c.ServerImage,
		SSHKey: keyName, Firewall: c.FirewallName, UserData: userData,
		Labels: r.natsLabels(), Network: r.natsNetwork(), Group: c.Group,
		Bootstrap: model.Bootstrap{AuthorizedKeys: trimmed(keys), HostKey: hk.Private, HostKeyPub: hk.Public, HostCertificate: hk.Certificate},
	})
	if err != nil {
		code, err := r.fail("creating the server %s failed: %s", c.ServerName, r.d.Cloud.Describe(err))
		return nil, false, code, err
	}
	r.printf("Server %s created.\n", c.ServerName)
	id := created.ID
	_ = os.WriteFile(c.IDState, []byte(id+"\n"), 0o600)
	r.lg.Event("server created id=%s", id)
	// a new server has none of the workspaces an earlier one with this name had
	_ = os.Remove(c.WorkspaceState) // best effort: may not exist
	_ = os.Remove(c.ReadyState)
	return r.ipOrFail(created, false)
}

func (r *runner) ipOrFail(s *model.Server, reused bool) (*model.Server, bool, int, error) {
	if s.IPv4 == "" && !s.ViaAPI { // a pod has no address: it is reached through the API of the cluster
		code, err := r.fail("server %s has no IPv4 address", r.c.ServerName)
		return nil, false, code, err
	}
	return s, reused, 0, nil
}

// The probe always exits 0 once connected; only connection failures are retried.
// cloud-init exits 0 (done), 2 (done with warnings) or 1 (errors).
// reboot=pending: updates were installed and cloud-init is about to restart the server.
const vmProbe = `cloud-init status --wait >/dev/null 2>&1; echo "cloud-init=$?"; test -f /var/tmp/claude-ready && echo claude=ok; docker info >/dev/null 2>&1 && echo docker=ok; test -f "${VIPER_REBOOT_FILE:-/var/run/reboot-required}" && echo reboot=pending; echo "ready=$(cat ~/.config/viper/ready 2>/dev/null)"; true`

// containerProbe is the probe for a pod: there is no cloud-init (the image has all), the entrypoint writes the ready
// file before sshd starts, Claude Code is in the image, and Docker answers once the sidecar is up (the pod is ready only
// then).
const containerProbe = `test -f /tmp/viper-ready && echo "cloud-init=0"; test -x "$HOME/.local/bin/claude" && echo claude=ok; docker info >/dev/null 2>&1 && echo docker=ok; echo "ready=$(cat ~/.config/viper/ready 2>/dev/null)"; true`

// probe is the readiness probe of the machine.
func (r *runner) probe() string {
	if r.c.IsContainer() {
		return containerProbe
	}
	return vmProbe
}

// dockerWanted tells whether the machine must have a running Docker: always on a VM, on a pod unless K8S_DOCKER=none.
func dockerWanted(c *model.Machine) bool {
	return !c.IsContainer() || c.DockerMode() != "none"
}

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
	switch model.SSHFailureKind(errMsg) {
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
	r.printf("Waiting for SSH%s ...\n", map[bool]string{false: " and cloud-init", true: ""}[r.c.IsContainer()])
	status, errMsg := "", ""
	var rw rebootWait
	for i := 1; i <= 120; i++ {
		if ctx.Err() != nil {
			return "", 1, driving.ErrShown
		}
		stdout, stderr, err := r.d.Run(ctx, "", "ssh", "-n", "-F", c.SSHConfig, "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", c.SSHAlias, r.probe())
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
		if reused && model.SSHFailureKind(errMsg) != "network" {
			break
		}
		r.d.Sleep(5 * time.Second)
	}
	if status != "" {
		return status, 0, nil
	}
	r.reportSSHFailure(errMsg)
	return "", 1, driving.ErrShown
}

// trimmed is the keys without the new line at the end of each.
func trimmed(keys []string) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = strings.TrimSpace(k)
	}
	return out
}
