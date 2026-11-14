package compose

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/pt9912/hetzner-viper/internal/filelock"
)

// A project is where the machines live (docs/plan/compose-gruppe.md): the machines of a viper group share one compose
// project and one compose.yaml; a machine that is in no group has a project of its own (and the file it always had).
// The key of the project is the server name, or g_<group> for a group: an underscore is in no name, so the two never meet.
type project struct {
	key   string
	group string // the viper group; "" for a machine of its own
}

func ownProject(server string) project { return project{key: server} }

func groupProject(group string) project { return project{key: groupKeyPrefix + group, group: group} }

const groupKeyPrefix = "g_"

// projectOfKey is the project a label value (viper-project) names.
func projectOfKey(key string) project {
	if g, ok := strings.CutPrefix(key, groupKeyPrefix); ok {
		return groupProject(g)
	}
	return ownProject(key)
}

func (p project) shared() bool { return p.group != "" }

// name is the name docker compose knows the project by (compose -p).
func (p project) name() string {
	if p.shared() {
		return "viper_" + p.key
	}
	return "viper-" + p.key
}

// service is the compose service of the machine, and sidecar the one of its Docker daemon. In a project of its own they
// are called machine and docker; in a group's file the machines need names of their own.
func (p project) service(server string) string {
	if p.shared() {
		return server
	}
	return serviceMachine
}

func (p project) sidecar(server string) string {
	if p.shared() {
		return dockerName(server)
	}
	return serviceDocker
}

// key names something the machine has in the compose file (a volume, a secret): a machine of its own uses the plain
// name, a machine in a group's file adds its server name.
func (p project) item(server, what string) string {
	if p.shared() {
		return server + "-" + what
	}
	return what
}

func (c *Client) projectDir(p project) string {
	return filepath.Join(c.opts.StateDir, "compose", p.key)
}

func (c *Client) composeFile(p project) string {
	return filepath.Join(c.projectDir(p), "compose.yaml")
}

// secretDir is where the keys of the machine are: next to the file, and in a group's directory one per machine.
func (c *Client) secretDir(p project, server string) string {
	if p.shared() {
		return filepath.Join(c.projectDir(p), "secret", server)
	}
	return filepath.Join(c.projectDir(p), "secret")
}

// markerDeleting is the file a viper down writes before it deletes anything: if the down is interrupted, the next viper
// up removes what is left, as the data of a machine that nobody wants any more. Volumes without the mark hold the data
// of a machine that nobody asked to delete, and are used again.
func (c *Client) markerDeleting(p project, server string) string {
	if p.shared() {
		return filepath.Join(c.projectDir(p), "deleting-"+server)
	}
	return filepath.Join(c.projectDir(p), "deleting")
}

// lock takes the lock of the project: two viper up or down of the same machine at once would fight over the container
// names (docs/plan/compose.md §7.9). For a group it only covers changes to the file, not the start of a machine.
func (c *Client) lock(p project) (func(), error) {
	if err := os.MkdirAll(filepath.Join(c.opts.StateDir, "compose"), 0o700); err != nil {
		return nil, err
	}
	return filelock.Lock(filepath.Join(c.opts.StateDir, "compose", p.key+".lock"))
}

// lockMachine takes the lock of one machine of a group, which a CreateServer holds through the start (the lock of the
// group only covers the change of the file): two viper up of the same machine at once must not drop each other's keys.
// It is always taken before the lock of the project. A machine of its own has the lock of its project only.
func (c *Client) lockMachine(p project, name string) (func(), error) {
	if !p.shared() {
		return func() {}, nil
	}
	return c.lock(ownProject(name))
}

// projectOf finds the project of the machine: the label viper-project of its container or of a volume it has (no label:
// the machine was made by a version that had a project for every machine), or else the directory that holds its keys.
// A machine that has nothing yet is in a project of its own.
func (c *Client) projectOf(ctx context.Context, name string) (project, error) {
	in, err := c.container(ctx, name)
	if err != nil {
		return project{}, err
	}
	if in != nil && in.Config.Labels[labelServer] == name {
		return projectFromLabels(in.Config.Labels, name), nil
	}
	if exists, labels, err := c.volumeLabels(ctx, homeVolume(name)); err != nil {
		return project{}, err
	} else if exists && labels[labelProject] != "" {
		return projectOfKey(labels[labelProject]), nil
	}
	if p, ok := c.projectWithKeys(name); ok {
		return p, nil
	}
	return ownProject(name), nil
}

func projectFromLabels(labels map[string]string, name string) project {
	if k := labels[labelProject]; k != "" {
		return projectOfKey(k)
	}
	return ownProject(name) // a machine of a version that had a project for each
}

// projectWithKeys looks for the keys of a machine on this computer: in the directory of its own project or in that of a
// group.
func (c *Client) projectWithKeys(name string) (project, bool) {
	if _, err := os.Stat(c.projectDir(ownProject(name))); err == nil {
		return ownProject(name), true
	}
	// no Glob: the path of the state directory may hold characters it takes for a pattern
	entries, _ := os.ReadDir(filepath.Join(c.opts.StateDir, "compose"))
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), groupKeyPrefix) {
			if st, err := os.Stat(filepath.Join(c.opts.StateDir, "compose", e.Name(), "secret", name)); err == nil && st.IsDir() {
				return projectOfKey(e.Name()), true
			}
		}
	}
	return project{}, false
}

// readFile reads the compose file of the project; ok is false if there is none.
func (c *Client) readFile(p project) (f composeFile, ok bool, err error) {
	b, err := os.ReadFile(c.composeFile(p))
	if os.IsNotExist(err) {
		return composeFile{}, false, nil
	}
	if err != nil {
		return composeFile{}, false, err
	}
	if err := yaml.Unmarshal(b, &f); err != nil {
		return composeFile{}, false, fmt.Errorf("the compose file %s: %w", c.composeFile(p), err)
	}
	return f, true, nil
}

// writeFileAtomic writes the file of the project by a temporary file and a rename, so that a docker compose that reads it
// at that moment sees the old one or the new one, never half of one.
func (c *Client) writeFileAtomic(p project, f composeFile) error {
	out, err := f.marshal()
	if err != nil {
		return err
	}
	dir := c.projectDir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "compose.yaml.tmp*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // gone after the rename; best effort otherwise
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), c.composeFile(p))
}

// mergeMachine puts the services, volumes and secrets of one machine into the file of a group, replacing what it had.
func (f *composeFile) mergeMachine(add composeFile) {
	if f.Services == nil {
		f.Services = map[string]service{}
	}
	if f.Volumes == nil {
		f.Volumes = map[string]volume{}
	}
	if f.Secrets == nil {
		f.Secrets = map[string]secret{}
	}
	for k, v := range add.Services {
		f.Services[k] = v
	}
	for k, v := range add.Volumes {
		f.Volumes[k] = v
	}
	for k, v := range add.Secrets {
		f.Secrets[k] = v
	}
	for k, v := range add.Networks {
		if f.Networks == nil {
			f.Networks = map[string]network{}
		}
		f.Networks[k] = v
	}
}

// dropMachine takes the services, volumes and secrets of one machine out of the file of a group.
func (f *composeFile) dropMachine(p project, server string) {
	delete(f.Services, p.service(server))
	delete(f.Services, p.sidecar(server))
	for _, w := range []string{"home", "brew"} {
		delete(f.Volumes, p.item(server, w))
	}
	for _, sf := range secretFiles() {
		delete(f.Secrets, p.item(server, sf))
	}
	if len(f.Services) == 0 {
		f.Networks = nil
	}
}

// has says whether the file has the service of the machine.
func (f composeFile) has(p project, server string) bool {
	_, ok := f.Services[p.service(server)]
	return ok
}

// servicesOf are the services of the machine that the file has: what docker compose up starts for it. A project of its
// own starts everything it has (nil).
func (c *Client) servicesOf(p project, server string) []string {
	if !p.shared() {
		return nil
	}
	f, ok, err := c.readFile(p)
	if err != nil || !ok {
		return []string{p.service(server)}
	}
	var out []string
	for _, s := range []string{p.service(server), p.sidecar(server)} {
		if _, ok := f.Services[s]; ok {
			out = append(out, s)
		}
	}
	return out
}

// machinesOf are the machines whose services the file of the group has or whose keys it has, sorted.
func (c *Client) machinesOf(p project) []string {
	seen := map[string]bool{}
	if f, ok, err := c.readFile(p); err == nil && ok {
		for name, s := range f.Services {
			if s.ContainerName == name && !strings.HasSuffix(name, "-docker") { // a machine, not its sidecar (checkName: no machine ends so)
				seen[name] = true
			}
		}
	}
	dirs, _ := os.ReadDir(filepath.Join(c.projectDir(p), "secret"))
	for _, d := range dirs {
		if d.IsDir() {
			seen[d.Name()] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
