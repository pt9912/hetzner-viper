package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

var _ cloud.Provider = (*Client)(nil)
var _ cloud.Leftovers = (*Client)(nil)

// EnsureLoginKey gives the key itself as its reference: Docker keeps no key objects, the key goes with the machine
// (into its authorized_keys file).
func (*Client) EnsureLoginKey(_ context.Context, _, publicKey string) (model.LoginKey, error) {
	return model.LoginKey{Ref: strings.TrimSpace(publicKey)}, nil
}

// EnsureFirewall does nothing and says the rules are complete: the container is reached with docker exec, so there is
// no port to open. SSH_ALLOWED_CIDRS does not apply.
func (*Client) EnsureFirewall(context.Context, string, []string) (model.FirewallResult, error) {
	return model.FirewallResult{Complete: true}, nil
}

// inspected is what the adapter reads of a container.
type inspected struct {
	ID      string    `json:"Id"`
	Created time.Time `json:"Created"`
	Config  struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Running bool `json:"Running"`
	} `json:"State"`
}

// container returns the container of that name or ID, nil if there is none; ErrNotOurs if viper did not make it.
func (c *Client) container(ctx context.Context, ref string) (*inspected, error) {
	out, err := c.docker(ctx, "container", "inspect", "--", ref)
	if notFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var list []inspected
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list) != 1 {
		return nil, fmt.Errorf("docker container inspect %s: unexpected answer", ref)
	}
	if list[0].Config.Labels[labelManaged] != "true" {
		return nil, fmt.Errorf("%w: container %s", ErrNotOurs, ref)
	}
	return &list[0], nil
}

func serverOf(name string, in *inspected) *model.Server {
	return &model.Server{ID: in.ID, Name: name, Created: in.Created, SSHPort: SSHPort, ViaAPI: true,
		GroupRuleMissing: in.Config.Labels[model.LabelNATSGroup] == ""}
}

// ServerByName returns the server of that name, nil if there is none.
func (c *Client) ServerByName(ctx context.Context, name string) (*model.Server, error) {
	if err := checkName(name); err != nil {
		return nil, err
	}
	in, err := c.container(ctx, name)
	if err != nil || in == nil {
		return nil, err
	}
	return serverOf(name, in), nil
}

// writeProject writes the keys and the compose file of the machine into its directory, which only the user can enter.
// The key files are readable by everyone: they are bind-mounted, so the container sees the owner and the mode of the
// file on the host, and builder (uid 1000) is not the user of the host (docs/plan/compose.md §3).
func (c *Client) writeProject(s model.ServerSpec, f composeFile) error {
	dir := c.projectDir(s.Name)
	if err := os.MkdirAll(filepath.Join(dir, "secret"), 0o700); err != nil {
		return err
	}
	b := s.Bootstrap
	content := map[string]string{
		"authorized_keys":   strings.Join(b.AuthorizedKeys, "\n") + "\n",
		"host_key":          strings.TrimRight(b.HostKey, "\n") + "\n",
		"host_key.pub":      strings.TrimSpace(b.HostKeyPub) + "\n",
		"host_key-cert.pub": strings.TrimSpace(b.HostCertificate) + "\n",
	}
	for name, text := range content {
		p := filepath.Join(dir, "secret", name)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil { //nolint:gosec // see above
			return err
		}
		if err := os.Chmod(p, 0o644); err != nil { //nolint:gosec // see above: WriteFile keeps the mode of an old file
			return err
		}
	}
	out, err := f.marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(c.composeFile(s.Name), out, 0o600)
}

// markerDeleting is the file a viper down writes before it deletes anything: if the down is interrupted, the next viper
// up removes what is left, as the data of a machine that nobody wants any more. Volumes without the mark hold the data
// of a machine that nobody asked to delete, and are used again.
func (c *Client) markerDeleting(name string) string {
	return filepath.Join(c.projectDir(name), "deleting")
}

// checkSpec says what is wrong with the spec before anything is made, and returns the size.
func (c *Client) checkSpec(s model.ServerSpec) (cpus float64, memMiB int, err error) {
	if err := checkName(s.Name); err != nil {
		return 0, 0, err
	}
	if c.opts.Image == "" {
		return 0, 0, fmt.Errorf("COMPOSE_IMAGE is not set: the image of the machine (viper image build builds it)")
	}
	if len(s.Bootstrap.AuthorizedKeys) == 0 || s.Bootstrap.HostKey == "" || s.Bootstrap.HostCertificate == "" {
		return 0, 0, fmt.Errorf("the machine has no keys to start with (ServerSpec.Bootstrap is empty)")
	}
	return sizeOf(s.Size)
}

// CreateServer writes the compose file and the keys and starts the machine, waiting until it is healthy.
func (c *Client) CreateServer(ctx context.Context, s model.ServerSpec) (*model.Server, error) {
	cpus, mem, err := c.checkSpec(s)
	if err != nil {
		return nil, err
	}
	unlock, err := c.lock(s.Name)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if in, err := c.container(ctx, s.Name); err != nil || in != nil {
		if err == nil {
			err = fmt.Errorf("the container %s exists: viper up uses it", s.Name)
		}
		return nil, err
	}
	if err := c.dropStale(ctx, s.Name); err != nil {
		return nil, err
	}
	if err := c.writeProject(s, c.opts.fileFor(s, cpus, mem)); err != nil {
		return nil, &cloud.OpError{Op: "writing the compose file of " + s.Name, Write: true, Err: err}
	}
	if err := c.up(ctx, s.Name); err != nil {
		return nil, &cloud.OpError{Op: "starting the machine " + s.Name, Write: true, Err: err}
	}
	in, err := c.container(ctx, s.Name)
	if err != nil || in == nil {
		if err == nil {
			err = fmt.Errorf("the container %s is not there after docker compose up", s.Name)
		}
		return nil, err
	}
	return serverOf(s.Name, in), nil
}

// up starts what the file describes and waits until it is healthy. An existing container is not made new
// (--no-recreate): a changed setting must not stop a running machine (docs/plan/compose.md §7.10).
func (c *Client) up(ctx context.Context, name string) error {
	_, err := c.compose(ctx, name, "up", "-d", "--no-recreate", "--wait", "--wait-timeout", fmt.Sprint(int(c.opts.timeout().Seconds())))
	return err
}

// EnsureRunning starts a machine that exists but is stopped, and repairs the Docker service of the machine if a restart
// of the machine alone has cut it off (docs/plan/compose.md §7.8). It does nothing to a machine that is fine.
func (c *Client) EnsureRunning(ctx context.Context, name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	in, err := c.container(ctx, name)
	if err != nil || in == nil {
		if err == nil {
			err = fmt.Errorf("the machine %s does not exist", name)
		}
		return err
	}
	if _, err := os.Stat(c.composeFile(name)); err != nil {
		return fmt.Errorf("the compose file of %s is not on this computer (%s): the machine was made elsewhere", name, c.composeFile(name))
	}
	unlock, err := c.lock(name)
	if err != nil {
		return err
	}
	defer unlock()
	if !in.State.Running {
		if err := c.up(ctx, name); err != nil {
			return err
		}
	}
	return c.repairDocker(ctx, name)
}

// repairDocker restarts the Docker service if it has lost its network: a restart of the machine alone (a crash, the
// restart of the daemon or of the host) leaves it in the old network namespace, with the loopback address only, and
// docker compose up does not mend that.
func (c *Client) repairDocker(ctx context.Context, name string) error {
	if c.opts.docker() != DockerDind {
		return nil
	}
	side, err := c.container(ctx, dockerName(name))
	if err != nil || side == nil || !side.State.Running {
		return err
	}
	if _, err := c.docker(ctx, "exec", dockerName(name), "test", "-e", "/sys/class/net/eth0"); err == nil {
		return nil
	}
	if _, err := c.docker(ctx, "restart", dockerName(name)); err != nil {
		return &cloud.OpError{Op: "restarting the Docker service of " + name, Write: true, Err: err}
	}
	return c.waitHealthy(ctx, dockerName(name))
}

// waitHealthy waits until the health check of the container passes: the daemon needs several seconds after a restart,
// and viper goes on with commands that need it.
func (c *Client) waitHealthy(ctx context.Context, name string) error {
	deadline := time.Now().Add(c.opts.timeout())
	for {
		out, err := c.docker(ctx, "container", "inspect", "--format", "{{if .State.Health}}{{.State.Health.Status}}{{end}}", "--", name)
		if err != nil {
			return err
		}
		if strings.TrimSpace(out) == "healthy" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s is not healthy after %s", name, c.opts.timeout())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// ours reports whether the volume carries viper's label. A volume that is not there is not ours and no error.
func (c *Client) volumeOurs(ctx context.Context, v string) (exists, ours bool, err error) {
	out, err := c.docker(ctx, "volume", "inspect", "--format", "{{json .Labels}}", "--", v)
	if notFound(err) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	var labels map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &labels); err != nil {
		return true, false, nil //nolint:nilerr // no labels is not ours
	}
	return true, labels[labelManaged] == "true", nil
}

// removeVolumes deletes the volumes of the machine that viper made. One that viper did not make is an error.
func (c *Client) removeVolumes(ctx context.Context, name string) error {
	for _, v := range []string{homeVolume(name), brewVolume(name)} {
		exists, ours, err := c.volumeOurs(ctx, v)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if !ours {
			return fmt.Errorf("%w: volume %s", ErrNotOurs, v)
		}
		if _, err := c.docker(ctx, "volume", "rm", "--", v); err != nil && !notFound(err) {
			return &cloud.OpError{Op: "deleting the volume " + v, Write: true, Err: err}
		}
	}
	return nil
}

// dropStale removes what a machine of that name left (a down that stopped half way): the keys, which are new ones for a
// new machine, and, only if a down had marked the machine as being deleted, its volumes. The caller holds the lock and
// has seen that the container does not exist.
func (c *Client) dropStale(ctx context.Context, name string) error {
	if err := c.removeSidecar(ctx, name); err != nil {
		return err
	}
	if _, err := os.Stat(c.markerDeleting(name)); err == nil {
		if err := c.removeVolumes(ctx, name); err != nil {
			return err
		}
	}
	return os.RemoveAll(c.projectDir(name))
}

// removeSidecar removes the container of the Docker service if viper made it and the machine is gone.
func (c *Client) removeSidecar(ctx context.Context, name string) error {
	side, err := c.container(ctx, dockerName(name))
	if err != nil || side == nil {
		return err
	}
	if _, err := c.docker(ctx, "container", "rm", "-f", "--", dockerName(name)); err != nil && !notFound(err) {
		return &cloud.OpError{Op: "removing the container " + dockerName(name), Write: true, Err: err}
	}
	return nil
}

// DeleteServer stops and removes the containers and deletes the volumes and the keys. The data of the machine is
// destroyed, as with a VM.
func (c *Client) DeleteServer(ctx context.Context, id string) error {
	in, err := c.container(ctx, id)
	if err != nil || in == nil {
		return err // nothing of that ID: a server that is gone is no error
	}
	name := in.Config.Labels[labelServer]
	if err := checkName(name); err != nil {
		return fmt.Errorf("container %s: %w", id, err)
	}
	unlock, err := c.lock(name)
	if err != nil {
		return err
	}
	defer unlock()
	if err := os.MkdirAll(c.projectDir(name), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(c.markerDeleting(name), nil, 0o600); err != nil {
		return err
	}
	if _, err := os.Stat(c.composeFile(name)); err == nil {
		if _, err := c.compose(ctx, name, "down", "--timeout", "10"); err != nil {
			return &cloud.OpError{Op: "stopping the machine " + name, Write: true, Err: err}
		}
	} else if _, err := c.docker(ctx, "container", "rm", "-f", "--", name, dockerName(name)); err != nil && !notFound(err) {
		return &cloud.OpError{Op: "removing the containers of " + name, Write: true, Err: err}
	}
	if err := c.removeVolumes(ctx, name); err != nil {
		return err
	}
	return os.RemoveAll(c.projectDir(name))
}

// Leftovers names what a machine of that name has left while its container is gone: the volumes with its data, the keys
// and the compose file, a Docker service that stayed behind.
func (c *Client) Leftovers(ctx context.Context, name string) ([]string, error) {
	if err := checkName(name); err != nil {
		return nil, err
	}
	if in, err := c.container(ctx, name); err != nil || in != nil {
		return nil, err
	}
	var out []string
	if side, err := c.container(ctx, dockerName(name)); err != nil {
		return nil, err
	} else if side != nil {
		out = append(out, "container "+dockerName(name))
	}
	for _, v := range []struct{ vol, what string }{{homeVolume(name), "home directory"}, {brewVolume(name), "Homebrew"}} {
		exists, ours, err := c.volumeOurs(ctx, v.vol)
		if err != nil {
			return nil, err
		}
		if exists && ours {
			out = append(out, fmt.Sprintf("volume %s (%s)", v.vol, v.what))
		}
	}
	if _, err := os.Stat(c.projectDir(name)); err == nil {
		out = append(out, "files "+c.projectDir(name))
	}
	return out, nil
}

// DeleteLeftovers deletes them; the machine must not exist.
func (c *Client) DeleteLeftovers(ctx context.Context, name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	unlock, err := c.lock(name)
	if err != nil {
		return err
	}
	defer unlock()
	if in, err := c.container(ctx, name); err != nil || in != nil {
		if err == nil {
			err = fmt.Errorf("the machine %s exists: viper down deletes it", name)
		}
		return err
	}
	if err := c.removeSidecar(ctx, name); err != nil {
		return err
	}
	if err := c.removeVolumes(ctx, name); err != nil {
		return err
	}
	return os.RemoveAll(c.projectDir(name))
}

// lock takes the lock of the project of the machine: two viper up or down of the same machine at once would fight over
// the container names (docs/plan/compose.md §7.9).
func (c *Client) lock(name string) (func(), error) {
	if err := os.MkdirAll(c.projectDir(name), 0o700); err != nil {
		return nil, err
	}
	return lockFile(filepath.Join(c.opts.StateDir, "compose", name+".lock"))
}
