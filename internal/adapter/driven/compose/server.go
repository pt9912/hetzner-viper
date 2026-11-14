package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pt9912/hetzner-viper/internal/filelock"
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
	Name    string    `json:"Name"`
	Created time.Time `json:"Created"`
	Config  struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Running bool `json:"Running"`
	} `json:"State"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
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

// machine returns the container of the machine of that name, nil if there is none. docker inspect also finds a container
// by the start of its ID and finds the Docker service of a machine by its own name, so the answer must carry the
// name of the machine as its label.
func (c *Client) machine(ctx context.Context, name string) (*inspected, error) {
	in, err := c.container(ctx, name)
	if err != nil || in == nil {
		return nil, err
	}
	if in.Config.Labels[labelServer] != name || strings.TrimPrefix(in.Name, "/") != name {
		return nil, nil
	}
	return in, nil
}

func serverOf(name string, in *inspected) *model.Server {
	s := &model.Server{ID: in.ID, Name: name, Created: in.Created, SSHPort: SSHPort, ViaAPI: true,
		GroupRuleMissing: in.Config.Labels[model.LabelNATSGroup] == ""}
	if g := in.Config.Labels[model.LabelNATSGroup]; g != "" { // the address in the network of the group
		s.PrivateIP = in.NetworkSettings.Networks[NetworkName(g)].IPAddress
	}
	return s
}

// ServerByName returns the server of that name, nil if there is none.
func (c *Client) ServerByName(ctx context.Context, name string) (*model.Server, error) {
	if err := checkName(name); err != nil {
		return nil, err
	}
	in, err := c.machine(ctx, name)
	if err != nil || in == nil {
		return nil, err
	}
	return serverOf(name, in), nil
}

// writeProject writes the keys of the machine into their directory, which only the user can enter, and its compose file:
// the file of its own project, or, in a group, the group's file with the machine put in (the caller holds the lock). The
// key files are readable by everyone: they are bind-mounted, so the container sees the owner and the mode of the file on
// the host, and builder (uid 1000) is not the user of the host (docs/plan/compose.md §3).
func (c *Client) writeProject(p project, s model.ServerSpec, f composeFile) error {
	dir := c.secretDir(p, s.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
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
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil { //nolint:gosec // see above
			return err
		}
		if err := os.Chmod(path, 0o644); err != nil { //nolint:gosec // see above: WriteFile keeps the mode of an old file
			return err
		}
	}
	if p.shared() {
		whole, _, err := c.readFile(p)
		if err != nil {
			return err
		}
		whole.mergeMachine(f)
		f = whole
	}
	return c.writeFileAtomic(p, f)
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

// CreateServer writes the compose file and the keys and starts the machine, waiting until it is healthy. The machines of a
// group share one project and one file: the lock only covers the change of the file, the start of the machine is not
// under it, so that the machines of a group start at the same time.
func (c *Client) CreateServer(ctx context.Context, s model.ServerSpec) (*model.Server, error) {
	cpus, mem, err := c.checkSpec(s)
	if err != nil {
		return nil, err
	}
	p, err := projectFor(s)
	if err != nil {
		return nil, err
	}
	unlock, err := c.lock(p)
	if err != nil {
		return nil, err
	}
	var once sync.Once
	release := func() { once.Do(unlock) }
	defer release()
	if in, err := c.machine(ctx, s.Name); err != nil || in != nil {
		if err == nil {
			err = fmt.Errorf("the container %s exists: viper up uses it", s.Name)
		}
		return nil, err
	}
	if err := c.dropStale(ctx, p, s.Name); err != nil {
		return nil, err
	}
	subnet, err := c.subnetOf(ctx, s.Network)
	if err != nil {
		return nil, err
	}
	if err := c.writeProject(p, s, c.opts.fileFor(p, s, cpus, mem, subnet)); err != nil {
		return nil, &cloud.OpError{Op: "writing the compose file of " + s.Name, Write: true, Err: err}
	}
	if p.shared() {
		release()
	}
	if err := c.up(ctx, p, s.Name); err != nil {
		return nil, &cloud.OpError{Op: "starting the machine " + s.Name, Write: true, Err: err}
	}
	in, err := c.machine(ctx, s.Name)
	if err != nil || in == nil {
		if err == nil {
			err = fmt.Errorf("the container %s is not there after docker compose up", s.Name)
		}
		return nil, err
	}
	return serverOf(s.Name, in), nil
}

// projectFor is the project a new machine is made in: the one of its group, or one of its own.
func projectFor(s model.ServerSpec) (project, error) {
	if s.Group == "" {
		return ownProject(s.Name), nil
	}
	if err := checkName(s.Group); err != nil {
		return project{}, fmt.Errorf("the group: %w", err)
	}
	return groupProject(s.Group), nil
}

// up starts what the file describes for the machine (a project of its own: all it has) and waits until it is healthy. An
// existing container is not made new (--no-recreate): a changed setting must not stop a running machine
// (docs/plan/compose.md §7.10).
func (c *Client) up(ctx context.Context, p project, name string) error {
	args := []string{"up", "-d", "--no-recreate", "--wait", "--wait-timeout", fmt.Sprint(int(c.opts.timeout().Seconds()))}
	_, err := c.compose(ctx, p, append(args, c.servicesOf(p, name)...)...)
	return err
}

var _ cloud.ImagePreparer = (*Client)(nil)

// PrepareImage pulls the image if the Docker host does not have it and its name says where it comes from (a registry host
// in front: ghcr.io/...). An image that was built here (viper image build) has no such name and is not pulled. Machines of
// a group start at the same time: the pull is done once.
func (c *Client) PrepareImage(ctx context.Context, out io.Writer) error {
	if c.opts.Image == "" {
		return fmt.Errorf("COMPOSE_IMAGE is not set: the image of the machine")
	}
	unlock, err := c.lockImage()
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := c.docker(ctx, "image", "inspect", "--format", "{{.Id}}", "--", c.opts.Image); err == nil {
		return nil
	}
	if !strings.Contains(c.opts.Image, "/") {
		return fmt.Errorf("the image %s is not on the Docker host and has no registry in its name to pull it from: viper image build builds it", c.opts.Image)
	}
	fmt.Fprintf(out, "The image %s is not on the Docker host: pulling it (several GB) ...\n", c.opts.Image)
	if _, err := c.docker(ctx, "pull", "--", c.opts.Image); err != nil {
		return err
	}
	return nil
}

func (c *Client) lockImage() (func(), error) {
	if err := os.MkdirAll(filepath.Join(c.opts.StateDir, "compose"), 0o700); err != nil {
		return nil, err
	}
	return filelock.Lock(filepath.Join(c.opts.StateDir, "compose", "image.lock"))
}

// EnsureRunning starts a machine that exists but is stopped, and repairs the Docker service of the machine if a restart
// of the machine alone has cut it off (docs/plan/compose.md §7.8). It does nothing to a machine that is fine.
func (c *Client) EnsureRunning(ctx context.Context, name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	p, err := c.projectOf(ctx, name)
	if err != nil {
		return err
	}
	unlock, err := c.lock(p) // before the look: a viper down that holds the lock may remove the machine meanwhile
	if err != nil {
		return err
	}
	var once sync.Once
	release := func() { once.Do(unlock) }
	defer release()
	in, err := c.machine(ctx, name)
	if err != nil || in == nil {
		if err == nil {
			err = fmt.Errorf("the machine %s does not exist", name)
		}
		return err
	}
	if !c.fileHas(p, name) {
		return fmt.Errorf("the compose file of %s is not on this computer (%s): the machine was made elsewhere", name, c.composeFile(p))
	}
	// a machine that is in use again is not being deleted: a mark left by a viper down that stopped half way must not
	// make a later viper up delete its volumes
	if err := os.Remove(c.markerDeleting(p, name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if p.shared() {
		release()
	}
	if !in.State.Running {
		if err := c.up(ctx, p, name); err != nil {
			return err
		}
	}
	return c.repairDocker(ctx, name)
}

// fileHas says whether the compose file of the project is there and, in a group, has the machine.
func (c *Client) fileHas(p project, name string) bool {
	if !p.shared() {
		_, err := os.Stat(c.composeFile(p))
		return err == nil
	}
	f, ok, err := c.readFile(p)
	return err == nil && ok && f.has(p, name)
}

// repairDocker restarts the Docker service if it has lost its network: a restart of the machine alone (a crash, the
// restart of the daemon or of the host) leaves it in the old network namespace, with the loopback address only, and
// docker compose up does not mend that.
func (c *Client) repairDocker(ctx context.Context, name string) error {
	if c.opts.docker() != DockerDind {
		return nil
	}
	side, err := c.container(ctx, dockerName(name))
	if err != nil || side == nil {
		return err
	}
	if !side.State.Running { // stopped alone: start it, and with a machine that runs it joins the network of the machine
		if _, err := c.docker(ctx, "start", dockerName(name)); err != nil {
			return &cloud.OpError{Op: "starting the Docker service of " + name, Write: true, Err: err}
		}
		return c.waitHealthy(ctx, dockerName(name))
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

// volumeLabels reads the labels of a volume. A volume that is not there is no error: exists is false.
func (c *Client) volumeLabels(ctx context.Context, v string) (exists bool, labels map[string]string, err error) {
	out, err := c.docker(ctx, "volume", "inspect", "--format", "{{json .Labels}}", "--", v)
	if notFound(err) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &labels); err != nil {
		return true, nil, nil //nolint:nilerr // no labels is not ours
	}
	return true, labels, nil
}

// volumeOurs reports whether the volume carries viper's label. A volume that is not there is not ours and no error.
func (c *Client) volumeOurs(ctx context.Context, v string) (exists, ours bool, err error) {
	exists, labels, err := c.volumeLabels(ctx, v)
	return exists, labels[labelManaged] == "true", err
}

// removeVolumes deletes the volumes of the machine that viper made: home directory, Homebrew and the NATS state. One
// that viper did not make is an error.
func (c *Client) removeVolumes(ctx context.Context, name string) error {
	states, err := c.stateVolumes(ctx, name)
	if err != nil {
		return err
	}
	for _, v := range append([]string{homeVolume(name), brewVolume(name)}, states...) {
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
func (c *Client) dropStale(ctx context.Context, p project, name string) error {
	if err := c.removeSidecar(ctx, name); err != nil {
		return err
	}
	if _, err := os.Stat(c.markerDeleting(p, name)); err == nil {
		if err := c.removeVolumes(ctx, name); err != nil {
			return err
		}
	}
	// The NATS state describes the container. Without one it is stale, whatever the mark says (a down that was cut off
	// leaves it): a new machine must not take it for its own. The data volumes are another matter and stay.
	if err := c.removeStateVolumes(ctx, name); err != nil {
		return err
	}
	return c.forgetMachine(p, name)
}

// forgetMachine removes what viper keeps of the machine on this computer: for a project of its own the whole directory,
// in a group the keys, the mark and the machine's part of the file; the directory goes with the last machine. The caller
// holds the lock, which also makes the decision "was that the last".
func (c *Client) forgetMachine(p project, name string) error {
	if !p.shared() {
		return os.RemoveAll(c.projectDir(p))
	}
	f, ok, err := c.readFile(p)
	if err != nil {
		return err
	}
	if ok {
		f.dropMachine(p, name)
		if len(f.Services) == 0 {
			if err := os.Remove(c.composeFile(p)); err != nil && !os.IsNotExist(err) {
				return err
			}
		} else if err := c.writeFileAtomic(p, f); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(c.secretDir(p, name)); err != nil {
		return err
	}
	if err := os.Remove(c.markerDeleting(p, name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(c.machinesOf(p)) == 0 {
		return os.RemoveAll(c.projectDir(p))
	}
	return nil
}

// removeStateVolumes deletes the NATS state volumes of the machine.
func (c *Client) removeStateVolumes(ctx context.Context, name string) error {
	states, err := c.stateVolumes(ctx, name)
	if err != nil {
		return err
	}
	for _, v := range states {
		if _, err := c.docker(ctx, "volume", "rm", "--", v); err != nil && !notFound(err) {
			return &cloud.OpError{Op: "deleting the stale NATS state " + v, Write: true, Err: err}
		}
	}
	return nil
}

// removeSidecar removes the container of the Docker service if viper made it and the machine is gone.
func (c *Client) removeSidecar(ctx context.Context, name string) error {
	side, err := c.container(ctx, dockerName(name))
	if err != nil || side == nil {
		return err
	}
	vol := c.daemonVolume(ctx, name)
	if _, err := c.docker(ctx, "container", "rm", "-f", "--", dockerName(name)); err != nil && !notFound(err) {
		return &cloud.OpError{Op: "removing the container " + dockerName(name), Write: true, Err: err}
	}
	return c.removeDaemonVolume(ctx, vol)
}

// daemonVolume is the name of the anonymous volume of /var/lib/docker of the Docker service ("" if there is none): the
// image declares it, and neither docker compose down nor docker rm removes it. It holds the images of the machine's
// Docker, which can be gigabytes. Read before the container goes.
func (c *Client) daemonVolume(ctx context.Context, name string) string {
	out, err := c.docker(ctx, "container", "inspect", "--format", `{{range .Mounts}}{{if eq .Destination "/var/lib/docker"}}{{.Name}}{{end}}{{end}}`, "--", dockerName(name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func (c *Client) removeDaemonVolume(ctx context.Context, vol string) error {
	if vol == "" {
		return nil
	}
	if _, err := c.docker(ctx, "volume", "rm", "--", vol); err != nil && !notFound(err) {
		return &cloud.OpError{Op: "deleting the volume " + vol, Write: true, Err: err}
	}
	return nil
}

// DeleteServer stops and removes the containers and deletes the volumes and the keys. The data of the machine is
// destroyed, as with a VM. In a group only the machine's services go (docker compose rm), the others run on.
func (c *Client) DeleteServer(ctx context.Context, id string) error {
	in, err := c.container(ctx, id)
	if err != nil || in == nil {
		return err // nothing of that ID: a server that is gone is no error
	}
	name := in.Config.Labels[labelServer]
	if err := checkName(name); err != nil {
		return fmt.Errorf("container %s: %w", id, err)
	}
	if strings.TrimPrefix(in.Name, "/") != name {
		return fmt.Errorf("%w: container %s is not the machine %s", ErrNotOurs, id, name)
	}
	p := projectFromLabels(in.Config.Labels, name)
	unlock, err := c.lock(p)
	if err != nil {
		return err
	}
	defer unlock()
	if err := os.MkdirAll(c.projectDir(p), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(c.markerDeleting(p, name), nil, 0o600); err != nil {
		return err
	}
	dataVolume := c.daemonVolume(ctx, name)
	if err := c.removeContainers(ctx, p, name); err != nil {
		return err
	}
	if err := c.removeDaemonVolume(ctx, dataVolume); err != nil {
		return err
	}
	if err := c.removeVolumes(ctx, name); err != nil {
		return err
	}
	return c.forgetMachine(p, name)
}

// removeContainers stops and removes the containers of the machine: through compose if the file has it (a group: only its
// services; -v takes the anonymous volume of the Docker daemon with it), else directly.
func (c *Client) removeContainers(ctx context.Context, p project, name string) error {
	switch {
	case !p.shared() && c.fileHas(p, name):
		if _, err := c.compose(ctx, p, "down", "--timeout", "10"); err != nil {
			return &cloud.OpError{Op: "stopping the machine " + name, Write: true, Err: err}
		}
	case p.shared() && c.fileHas(p, name):
		args := append([]string{"rm", "--stop", "--force", "--volumes"}, c.servicesOf(p, name)...)
		if _, err := c.compose(ctx, p, args...); err != nil {
			return &cloud.OpError{Op: "stopping the machine " + name, Write: true, Err: err}
		}
	default:
		if _, err := c.docker(ctx, "container", "rm", "-f", "--", name, dockerName(name)); err != nil && !notFound(err) {
			return &cloud.OpError{Op: "removing the containers of " + name, Write: true, Err: err}
		}
	}
	return nil
}

// Leftovers names what a machine of that name has left while its container is gone: the volumes with its data, the keys
// and the compose file, a Docker service that stayed behind.
func (c *Client) Leftovers(ctx context.Context, name string) ([]string, error) {
	if err := checkName(name); err != nil {
		return nil, err
	}
	if in, err := c.machine(ctx, name); err != nil || in != nil {
		return nil, err
	}
	p, err := c.projectOf(ctx, name)
	if err != nil {
		return nil, err
	}
	var out []string
	if side, err := c.container(ctx, dockerName(name)); err != nil {
		return nil, err
	} else if side != nil {
		out = append(out, "container "+dockerName(name))
	}
	states, err := c.stateVolumes(ctx, name)
	if err != nil {
		return nil, err
	}
	vols := []struct{ vol, what string }{{homeVolume(name), "home directory"}, {brewVolume(name), "Homebrew"}}
	for _, v := range states {
		vols = append(vols, struct{ vol, what string }{v, "NATS state"})
	}
	for _, v := range vols {
		exists, ours, err := c.volumeOurs(ctx, v.vol)
		if err != nil {
			return nil, err
		}
		if exists && ours {
			out = append(out, fmt.Sprintf("volume %s (%s)", v.vol, v.what))
		}
	}
	// a group's directory is not a leftover of one machine while the others live in it: its keys and its part of the file are
	if dir := c.leftoverFiles(p, name); dir != "" {
		out = append(out, "files "+dir)
	}
	return out, nil
}

// leftoverFiles is what viper keeps of the machine on this computer ("" if nothing).
func (c *Client) leftoverFiles(p project, name string) string {
	if !p.shared() {
		if _, err := os.Stat(c.projectDir(p)); err == nil {
			return c.projectDir(p)
		}
		return ""
	}
	if _, err := os.Stat(c.secretDir(p, name)); err == nil || c.fileHas(p, name) {
		return c.secretDir(p, name)
	}
	return ""
}

// DeleteLeftovers deletes them; the machine must not exist.
func (c *Client) DeleteLeftovers(ctx context.Context, name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	p, err := c.projectOf(ctx, name)
	if err != nil {
		return err
	}
	unlock, err := c.lock(p)
	if err != nil {
		return err
	}
	defer unlock()
	if in, err := c.machine(ctx, name); err != nil || in != nil {
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
	return c.forgetMachine(p, name)
}
