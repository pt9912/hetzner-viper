package compose_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/compose"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

// host is a fake Docker host: the docker program as the adapter calls it. It knows containers and volumes, and
// docker compose up/down make and remove them from the compose file it is given.
type host struct {
	mu         sync.Mutex
	containers map[string]*ctr // by name
	volumes    map[string]map[string]string
	calls      []string
	ups        int
	failUp     string
	noEth0     map[string]bool // containers that have lost eth0
	endpoint   string
	noCompose  bool
	noContext  bool // docker cannot find the context
	noImage    bool
	networks   int
	nets       map[string]map[string]string // docker networks by name, with their labels
}

type ctr struct {
	id     string
	labels map[string]string
	run    bool
	nets   []string
	anon   string // the anonymous volume of the container
}

func newHost() *host {
	return &host{containers: map[string]*ctr{}, volumes: map[string]map[string]string{}, nets: map[string]map[string]string{}, noEth0: map[string]bool{}, endpoint: "unix:///var/run/docker.sock"}
}

func (h *host) record(args []string) { h.calls = append(h.calls, strings.Join(args, " ")) }

func (h *host) called(sub string) int {
	n := 0
	for _, c := range h.calls {
		if strings.Contains(c, sub) {
			n++
		}
	}
	return n
}

func nosuch(what, name string) (string, string, error) {
	return "", "Error: No such " + what + ": " + name, errors.New("exit status 1")
}

func (h *host) run(_ context.Context, _, name string, args ...string) (string, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if name != "docker" || len(args) < 3 || args[0] != "--context" {
		return "", "", fmt.Errorf("unexpected call %s %v", name, args)
	}
	args = args[2:]
	h.record(args)
	switch {
	case args[0] == "version" && h.noContext:
		return "", "Failed to initialize: unable to resolve docker endpoint: context \"fsn1\": context not found: open meta.json: no such file or directory", errors.New("exit status 1")
	case args[0] == "version":
		return "29.9.3\n", "", nil
	case args[0] == "context":
		return h.endpoint + "\n", "", nil
	case args[0] == "compose" && args[1] == "version":
		if h.noCompose {
			return "", "docker: 'compose' is not a docker command", errors.New("exit status 125")
		}
		return "5.7.0\n", "", nil
	case args[0] == "compose":
		return h.compose(args)
	case args[0] == "pull":
		h.noImage = false
		return "", "", nil
	case args[0] == "image":
		if h.noImage {
			return nosuch("image", args[len(args)-1])
		}
		return "sha256:1\n", "", nil
	case args[0] == "network" && args[1] == "ls":
		return strings.Repeat("abc\n", h.networks), "", nil
	case args[0] == "network":
		return h.network(args)
	case args[0] == "container" && args[1] == "ls":
		var ids []string
		for _, c := range h.containers {
			if matches(c.labels, filters(args)) {
				ids = append(ids, c.id)
			}
		}
		return strings.Join(ids, "\n"), "", nil
	case args[0] == "volume" && args[1] == "create":
		labels := map[string]string{}
		for i, a := range args {
			if a == "--label" {
				kv := strings.SplitN(args[i+1], "=", 2)
				labels[kv[0]] = kv[1]
			}
		}
		h.volumes[args[len(args)-1]] = labels
		return "", "", nil
	case args[0] == "volume" && args[1] == "ls":
		var out []string
		f := filters(args)
		for n, l := range h.volumes {
			if !matches(l, f) {
				continue
			}
			if i := indexOf(args, "--format"); i >= 0 {
				out = append(out, l["viper-nats-state"])
			} else {
				out = append(out, n)
			}
		}
		return strings.Join(out, "\n"), "", nil
	case args[0] == "container" && args[1] == "inspect" && args[2] == "--format":
		if strings.Contains(args[3], "Health") {
			return "healthy\n", "", nil
		}
		if c, ok := h.containers[strings.TrimPrefix(args[len(args)-1], "")]; ok && strings.Contains(args[3], "Mounts") && c.anon != "" {
			return c.anon + "\n", "", nil // the anonymous /var/lib/docker volume
		}
		return "\n", "", nil
	case args[0] == "start":
		h.containers[args[1]].run = true
		return "", "", nil
	case args[0] == "container" && args[1] == "inspect":
		return h.inspect(args[len(args)-1])
	case args[0] == "container" && args[1] == "rm":
		for _, n := range args[3:] {
			delete(h.containers, n)
		}
		return "", "", nil
	case args[0] == "volume" && args[1] == "inspect":
		v, ok := h.volumes[args[len(args)-1]]
		if !ok {
			return nosuch("volume", args[len(args)-1])
		}
		b, _ := json.Marshal(v)
		return string(b), "", nil
	case args[0] == "volume" && args[1] == "rm":
		delete(h.volumes, args[len(args)-1])
		return "", "", nil
	case args[0] == "exec":
		if h.noEth0[args[1]] {
			return "", "", errors.New("exit status 1")
		}
		return "", "", nil
	case args[0] == "restart":
		delete(h.noEth0, args[1])
		return "", "", nil
	}
	return "", "", fmt.Errorf("fake docker: unknown call %v", args)
}

func indexOf(args []string, a string) int {
	for i, x := range args {
		if x == a {
			return i
		}
	}
	return -1
}

// filters are the label filters of a docker command, as key or key=value.
func filters(args []string) []string {
	var out []string
	for i, a := range args {
		if a == "--filter" {
			out = append(out, strings.TrimPrefix(args[i+1], "label="))
		}
	}
	return out
}

func matches(labels map[string]string, f []string) bool {
	for _, x := range f {
		kv := strings.SplitN(x, "=", 2)
		v, ok := labels[kv[0]]
		if !ok || (len(kv) == 2 && v != kv[1]) {
			return false
		}
	}
	return true
}

func (h *host) network(args []string) (string, string, error) {
	name := args[len(args)-1]
	switch args[1] {
	case "create":
		if _, ok := h.nets[name]; ok {
			return "", "Error response from daemon: network with name " + name + " already exists", errors.New("exit status 1")
		}
		labels := map[string]string{}
		for i, a := range args {
			if a == "--label" {
				kv := strings.SplitN(args[i+1], "=", 2)
				labels[kv[0]] = kv[1]
			}
		}
		h.nets[name] = labels
		return "id\n", "", nil
	case "inspect":
		l, ok := h.nets[name]
		if !ok {
			return nosuch("network", name)
		}
		b, _ := json.Marshal([]map[string]any{{"Id": "net-" + name, "Name": name, "Labels": l,
			"IPAM": map[string]any{"Config": []map[string]string{{"Subnet": "10.9.0.0/16"}}}}})
		return string(b), "", nil
	case "rm":
		for _, c := range h.containers {
			for _, n := range c.nets {
				if n == name {
					return "", "Error response from daemon: network " + name + " has active endpoints", errors.New("exit status 1")
				}
			}
		}
		delete(h.nets, name)
		return "", "", nil
	}
	return "", "", fmt.Errorf("unknown network call %v", args)
}

func (h *host) inspect(ref string) (string, string, error) {
	for n, c := range h.containers {
		if n == ref || c.id == ref || (len(ref) >= 4 && !strings.Contains(ref, "-") && strings.HasPrefix(c.id, ref)) {
			nets := map[string]any{}
			for _, x := range c.nets {
				nets[x] = map[string]string{"IPAddress": "10.9.0.5"}
			}
			b, _ := json.Marshal([]map[string]any{{
				"Id": c.id, "Name": "/" + n, "Created": time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC),
				"Config": map[string]any{"Labels": c.labels}, "State": map[string]any{"Running": c.run},
				"NetworkSettings": map[string]any{"Networks": nets},
			}})
			return string(b), "", nil
		}
	}
	return nosuch("container", ref)
}

type fileSvc struct {
	ContainerName string                    `yaml:"container_name"`
	Labels        map[string]string         `yaml:"labels"`
	Networks      map[string]map[string]any `yaml:"networks"`
}

func (h *host) compose(args []string) (string, string, error) {
	file := args[4]
	data, err := os.ReadFile(file)
	if err != nil {
		return "", "", err
	}
	var f struct {
		Services map[string]fileSvc `yaml:"services"`
		Volumes  map[string]struct {
			Name   string            `yaml:"name"`
			Labels map[string]string `yaml:"labels"`
		} `yaml:"volumes"`
	}
	if err := yaml.Unmarshal(data, &f); err != nil {
		return "", "", err
	}
	switch args[5] {
	case "up":
		h.ups++
		if h.failUp != "" {
			return "", h.failUp, errors.New("exit status 1")
		}
		for _, v := range f.Volumes {
			if _, ok := h.volumes[v.Name]; !ok {
				h.volumes[v.Name] = v.Labels
			}
		}
		for _, s := range f.Services {
			if c, ok := h.containers[s.ContainerName]; ok {
				c.run = true
				continue // --no-recreate
			}
			var nets []string
			for range s.Networks {
				nets = append(nets, "viper-nats-"+s.Labels["viper-nats-group"])
			}
			c := &ctr{id: "id-" + s.ContainerName, labels: s.Labels, run: true, nets: nets}
			if strings.HasSuffix(s.ContainerName, "-docker") { // docker:dind declares VOLUME /var/lib/docker
				c.anon = "anon-" + s.ContainerName
				h.volumes[c.anon] = map[string]string{}
			}
			h.containers[s.ContainerName] = c
		}
		return "", "", nil
	case "down":
		for _, s := range f.Services {
			delete(h.containers, s.ContainerName)
		}
		return "", "", nil
	}
	return "", "", fmt.Errorf("unknown compose call %v", args)
}

func spec(name string) model.ServerSpec {
	return model.ServerSpec{Name: name, Size: "2x4", Bootstrap: model.Bootstrap{
		AuthorizedKeys: []string{"ssh-ed25519 AAAA key"}, HostKey: "PRIVATE", HostKeyPub: "ssh-ed25519 HOST", HostCertificate: "ssh-ed25519-cert CERTBODY"}}
}

func newClient(t *testing.T, h *host, mod ...func(*compose.Options)) (*compose.Client, string) {
	t.Helper()
	dir := t.TempDir()
	o := compose.Options{Context: "default", Image: "viper-machine:test", StateDir: dir, DockerExplicit: true}
	for _, m := range mod {
		m(&o)
	}
	return compose.New(o, h.run), dir
}

func TestCreateWritesFileAndKeysAndStarts(t *testing.T) {
	h := newHost()
	c, dir := newClient(t, h)
	srv, err := c.CreateServer(context.Background(), spec("viper-web"))
	if err != nil {
		t.Fatal(err)
	}
	if srv.ID != "id-viper-web" || srv.SSHPort != 2222 || !srv.ViaAPI || srv.Name != "viper-web" {
		t.Fatalf("server %+v", srv)
	}
	if h.containers["viper-web-docker"] == nil || len(h.volumes) != 3 { // home, Homebrew and the anonymous volume of the daemon
		t.Fatalf("containers %v volumes %v", h.containers, h.volumes)
	}
	if h.called("compose -p viper-viper-web -f "+filepath.Join(dir, "compose", "viper-web", "compose.yaml")+" up -d --no-recreate --wait") != 1 {
		t.Fatalf("calls %v", h.calls)
	}
	key, err := os.ReadFile(filepath.Join(dir, "compose", "viper-web", "secret", "host_key"))
	if err != nil || string(key) != "PRIVATE\n" {
		t.Fatalf("key %q %v", key, err)
	}
	if st, _ := os.Stat(filepath.Join(dir, "compose", "viper-web")); st.Mode().Perm() != 0o700 && !isWindows() {
		t.Fatalf("project dir mode %v", st.Mode())
	}
}

func TestComposeFileIsStableAndHasNoSecrets(t *testing.T) {
	h := newHost()
	c, dir := newClient(t, h)
	if _, err := c.CreateServer(context.Background(), spec("viper-web")); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "compose", "viper-web", "compose.yaml")
	first, _ := os.ReadFile(p)
	for i := 0; i < 5; i++ { // the same input gives the same file, byte for byte
		h2 := newHost()
		c2, dir2 := newClient(t, h2)
		if _, err := c2.CreateServer(context.Background(), spec("viper-web")); err != nil {
			t.Fatal(err)
		}
		again, _ := os.ReadFile(filepath.Join(dir2, "compose", "viper-web", "compose.yaml"))
		if string(again) != string(first) {
			t.Fatalf("file changed:\n%s\n---\n%s", first, again)
		}
	}
	s := string(first)
	for _, bad := range []string{"PRIVATE", "AAAA key", "CERTBODY"} {
		if strings.Contains(s, bad) {
			t.Fatalf("the compose file has a key (%s):\n%s", bad, s)
		}
	}
	for _, want := range []string{"pull_policy: never", "container_name: viper-web", "network_mode: service:machine", "privileged: true",
		"target: /run/viper/secret/host_key", "restart: true", "DOCKER_HOST: tcp://127.0.0.1:2375", "viper-managed", "name: viper-web-home", "name: viper-web-brew", "mem_limit: 4096m", "cpus: 2"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	if strings.Contains(s, "networks:") {
		t.Errorf("a machine without a group has no networks:\n%s", s)
	}
}

func TestDockerModes(t *testing.T) {
	for mode, want := range map[string]struct {
		sidecar bool
		env     string
	}{
		compose.DockerDind: {true, "tcp://127.0.0.1:2375"}, compose.DockerRemote: {false, "tcp://far:2375"}, compose.DockerNone: {false, ""},
	} {
		h := newHost()
		c, dir := newClient(t, h, func(o *compose.Options) { o.Docker, o.DockerHost = mode, "tcp://far:2375" })
		if _, err := c.CreateServer(context.Background(), spec("m")); err != nil {
			t.Fatal(mode, err)
		}
		b, _ := os.ReadFile(filepath.Join(dir, "compose", "m", "compose.yaml"))
		if (h.containers["m-docker"] != nil) != want.sidecar || strings.Contains(string(b), "DOCKER_HOST") != (want.env != "") ||
			(want.env != "" && !strings.Contains(string(b), want.env)) || strings.Contains(string(b), "privileged") != want.sidecar {
			t.Errorf("%s: sidecar=%v\n%s", mode, h.containers["m-docker"] != nil, b)
		}
	}
}

func TestGroupNetworkAndLabelsGoIntoTheFile(t *testing.T) {
	h := newHost()
	c, dir := newClient(t, h)
	if _, _, err := c.EnsurePrivateNetwork(context.Background(), "g", "default"); err != nil {
		t.Fatal(err)
	}
	s := spec("web")
	s.Network = "viper-nats-g"
	s.Labels = map[string]string{model.LabelNATSGroup: "g", model.LabelNATSRole: "client"}
	srv, err := c.CreateServer(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "compose", "web", "compose.yaml"))
	for _, want := range []string{"external: true", "name: viper-nats-g", "viper-nats-group: g"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q in\n%s", want, b)
		}
	}
	if srv.GroupRuleMissing {
		t.Error("a machine with the group label has the group rule")
	}
}

func TestServerByNameAndNotOurs(t *testing.T) {
	h := newHost()
	c, _ := newClient(t, h)
	if s, err := c.ServerByName(context.Background(), "web"); s != nil || err != nil {
		t.Fatalf("%v %v", s, err)
	}
	h.containers["web"] = &ctr{id: "x", labels: map[string]string{}}
	if _, err := c.ServerByName(context.Background(), "web"); !errors.Is(err, compose.ErrNotOurs) {
		t.Fatalf("%v", err)
	}
	if _, err := c.ServerByName(context.Background(), "../x"); err == nil {
		t.Fatal("a name with a slash is accepted")
	}
}

func TestCreateRefusesWhatIsWrong(t *testing.T) {
	h := newHost()
	c, _ := newClient(t, h, func(o *compose.Options) { o.Image = "" })
	if _, err := c.CreateServer(context.Background(), spec("m")); err == nil || !strings.Contains(err.Error(), "COMPOSE_IMAGE") {
		t.Fatalf("%v", err)
	}
	c, _ = newClient(t, h)
	bad := spec("m")
	bad.Size = "big"
	if _, err := c.CreateServer(context.Background(), bad); err == nil {
		t.Fatal("size accepted")
	}
	bad = spec("m")
	bad.Bootstrap = model.Bootstrap{}
	if _, err := c.CreateServer(context.Background(), bad); err == nil {
		t.Fatal("no keys accepted")
	}
	c, _ = newClient(t, h, func(o *compose.Options) { o.Context = "" })
	if _, err := c.CreateServer(context.Background(), spec("m")); err == nil || !strings.Contains(err.Error(), "SERVER_LOCATION") {
		t.Fatalf("%v", err)
	}
	c, _ = newClient(t, h, func(o *compose.Options) { o.Context = "--host=evil" })
	if _, err := c.CreateServer(context.Background(), spec("m")); err == nil {
		t.Fatal("a context that begins with a dash is accepted")
	}
	if h.ups != 0 {
		t.Fatal("compose up ran")
	}
}

func TestUpFailureNamesDockersWords(t *testing.T) {
	h := newHost()
	h.failUp = "container m is unhealthy"
	c, _ := newClient(t, h)
	_, err := c.CreateServer(context.Background(), spec("m"))
	if err == nil || !strings.Contains(err.Error(), "unhealthy") {
		t.Fatalf("%v", err)
	}
}

func TestDeleteRemovesEverythingAndRefusesStrangers(t *testing.T) {
	h := newHost()
	c, dir := newClient(t, h)
	srv, err := c.CreateServer(context.Background(), spec("web"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteServer(context.Background(), srv.ID); err != nil {
		t.Fatal(err)
	}
	if len(h.containers) != 0 || len(h.volumes) != 0 {
		t.Fatalf("left: %v %v", h.containers, h.volumes)
	}
	if _, err := os.Stat(filepath.Join(dir, "compose", "web")); !os.IsNotExist(err) {
		t.Fatal("the project directory is still there")
	}
	if err := c.DeleteServer(context.Background(), srv.ID); err != nil {
		t.Fatalf("a server that is gone is an error: %v", err)
	}
	h.containers["other"] = &ctr{id: "o", labels: map[string]string{}}
	if err := c.DeleteServer(context.Background(), "o"); !errors.Is(err, compose.ErrNotOurs) {
		t.Fatalf("%v", err)
	}
	if h.containers["other"] == nil {
		t.Fatal("deleted a container that is not viper's")
	}
}

func TestDeleteDoesNotTouchAVolumeOfAnother(t *testing.T) {
	h := newHost()
	c, _ := newClient(t, h)
	srv, err := c.CreateServer(context.Background(), spec("web"))
	if err != nil {
		t.Fatal(err)
	}
	h.volumes["web-brew"] = map[string]string{} // not viper's
	if err := c.DeleteServer(context.Background(), srv.ID); !errors.Is(err, compose.ErrNotOurs) {
		t.Fatalf("%v", err)
	}
	if _, ok := h.volumes["web-brew"]; !ok {
		t.Fatal("deleted a volume that is not viper's")
	}
}

func TestLeftoversAndTheirDeletion(t *testing.T) {
	h := newHost()
	c, dir := newClient(t, h)
	srv, err := c.CreateServer(context.Background(), spec("web"))
	if err != nil {
		t.Fatal(err)
	}
	if l, err := c.Leftovers(context.Background(), "web"); err != nil || len(l) != 0 {
		t.Fatalf("a machine that exists has no leftovers: %v %v", l, err)
	}
	if err := c.DeleteLeftovers(context.Background(), "web"); err == nil {
		t.Fatal("leftovers of an existing machine deleted")
	}
	delete(h.containers, "web") // removed by hand: the sidecar, the volumes and the files stay
	l, err := c.Leftovers(context.Background(), "web")
	if err != nil || len(l) != 4 {
		t.Fatalf("%v %v", l, err)
	}
	if err := c.DeleteLeftovers(context.Background(), "web"); err != nil {
		t.Fatal(err)
	}
	if len(h.containers) != 0 || len(h.volumes) != 0 {
		t.Fatalf("left: %v %v", h.containers, h.volumes)
	}
	if _, err := os.Stat(filepath.Join(dir, "compose", "web")); !os.IsNotExist(err) {
		t.Fatal("files are still there")
	}
	_ = srv
}

// A machine whose container was removed by hand keeps its volumes (its data); a new machine of the name uses them,
// unless a viper down had marked it as being deleted.
func TestCreateKeepsOrDropsOldVolumes(t *testing.T) {
	h := newHost()
	c, dir := newClient(t, h)
	if _, err := c.CreateServer(context.Background(), spec("web")); err != nil {
		t.Fatal(err)
	}
	h.volumes["web-home"]["mark"] = "data"
	delete(h.containers, "web")
	delete(h.containers, "web-docker")
	if _, err := c.CreateServer(context.Background(), spec("web")); err != nil {
		t.Fatal(err)
	}
	if h.volumes["web-home"]["mark"] != "data" {
		t.Fatal("the data of a machine that nobody asked to delete is gone")
	}
	// a down that stopped half way
	delete(h.containers, "web")
	delete(h.containers, "web-docker")
	if err := os.WriteFile(filepath.Join(dir, "compose", "web", "deleting"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateServer(context.Background(), spec("web")); err != nil {
		t.Fatal(err)
	}
	if h.volumes["web-home"]["mark"] == "data" {
		t.Fatal("volumes of a machine marked as being deleted are used again")
	}
}

func TestEnsureRunningRepairsTheDockerService(t *testing.T) {
	h := newHost()
	c, _ := newClient(t, h)
	if _, err := c.CreateServer(context.Background(), spec("web")); err != nil {
		t.Fatal(err)
	}
	h.ups = 0
	if err := c.EnsureRunning(context.Background(), "web"); err != nil {
		t.Fatal(err)
	}
	if h.ups != 0 || h.called("restart") != 0 {
		t.Fatalf("a machine that is fine was touched: ups=%d %v", h.ups, h.calls)
	}
	h.noEth0["web-docker"] = true // docker restart web
	if err := c.EnsureRunning(context.Background(), "web"); err != nil {
		t.Fatal(err)
	}
	if h.called("restart web-docker") != 1 || h.called("{{.State.Health.Status}}") < 1 {
		t.Fatalf("the Docker service was not restarted: %v", h.calls)
	}
	h.containers["web"].run = false // stopped
	if err := c.EnsureRunning(context.Background(), "web"); err != nil {
		t.Fatal(err)
	}
	if h.ups != 1 || !h.containers["web"].run {
		t.Fatalf("a stopped machine was not started: ups=%d", h.ups)
	}
}

func TestEnsureRunningNeedsTheFileOfThisComputer(t *testing.T) {
	h := newHost()
	c, dir := newClient(t, h)
	if _, err := c.CreateServer(context.Background(), spec("web")); err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(filepath.Join(dir, "compose", "web"))
	if err := c.EnsureRunning(context.Background(), "web"); err == nil || !strings.Contains(err.Error(), "compose file") {
		t.Fatalf("%v", err)
	}
}

func TestValidate(t *testing.T) {
	has := func(f []model.Finding, l model.Level, sub string) bool {
		for _, x := range f {
			if x.Level == l && strings.Contains(x.Text, sub) {
				return true
			}
		}
		return false
	}
	ch := model.Check{Spec: spec("web"), ServerName: "web"}
	h := newHost()
	c, _ := newClient(t, h)
	f := c.Validate(context.Background(), ch)
	for _, want := range []string{"Docker host default answers", "docker compose 5.7.0", "image viper-machine:test", "will be created"} {
		if !has(f, model.OK, want) && !has(f, model.Info, want) {
			t.Errorf("missing %q in %v", want, f)
		}
	}
	if !has(f, model.Warn, "privileged") {
		t.Errorf("no warning for dind: %v", f)
	}

	c, _ = newClient(t, h, func(o *compose.Options) { o.DockerExplicit = false })
	if f := c.Validate(context.Background(), ch); !has(f, model.Fail, "privileged") {
		t.Errorf("dind by default is no failure: %v", f)
	}

	h.endpoint, h.noCompose, h.noImage, h.networks = "ssh://me@far", true, true, 25
	c, _ = newClient(t, h)
	f = c.Validate(context.Background(), ch)
	for _, want := range []string{"only a daemon on this computer", "compose plugin is missing", "is not on the Docker host"} {
		if !has(f, model.Fail, want) {
			t.Errorf("missing failure %q in %v", want, f)
		}
	}
	if !has(f, model.Warn, "25 networks") {
		t.Errorf("no warning for many networks: %v", f)
	}
}

func TestAnUnknownDockerContextIsExplained(t *testing.T) {
	h := newHost()
	h.noContext = true
	c, _ := newClient(t, h)
	f := c.Validate(context.Background(), model.Check{Spec: spec("web"), ServerName: "web"})
	var fail, hint bool
	for _, x := range f {
		fail = fail || x.Level == model.Fail && strings.Contains(x.Text, "Docker not usable")
		hint = hint || x.Level == model.Info && strings.Contains(x.Text, "SERVER_LOCATION is the Docker context")
	}
	if !fail || !hint {
		t.Errorf("fail %v hint %v: %v", fail, hint, f)
	}
}

func isWindows() bool { return os.PathSeparator == '\\' }

// --- NATS groups

func natsSpec(name, group, role string) model.ServerSpec {
	sp := spec(name)
	sp.Network = "viper-nats-" + group
	sp.Labels = model.NATSLabels(model.NATSMetadata{Group: group, Role: model.NATSRole(role), State: model.NATSProvisioning})
	return sp
}

func TestTheGroupNetworkIsMadeOnceAndFoundByTheOthers(t *testing.T) {
	h := newHost()
	c, _ := newClient(t, h)
	n, created, err := c.EnsurePrivateNetwork(context.Background(), "g", "default")
	if err != nil || !created || n.ID != "viper-nats-g" || n.IPRange != "10.9.0.0/16" || n.Zone != "default" {
		t.Fatalf("%+v %v %v", n, created, err)
	}
	if n2, created, err := c.EnsurePrivateNetwork(context.Background(), "g", "default"); err != nil || created || n2.ID != n.ID {
		t.Fatalf("second: %+v %v %v", n2, created, err)
	}
	if f, err := c.FindPrivateNetwork(context.Background(), "g"); err != nil || f == nil {
		t.Fatalf("%v %v", f, err)
	}
	if f, err := c.FindPrivateNetwork(context.Background(), "other"); err != nil || f != nil {
		t.Fatalf("%v %v", f, err)
	}
	h.nets["viper-nats-x"] = map[string]string{} // somebody else's
	if _, _, err := c.EnsurePrivateNetwork(context.Background(), "x", "default"); !errors.Is(err, compose.ErrNotOurs) {
		t.Fatalf("%v", err)
	}
	if f, err := c.FindPrivateNetwork(context.Background(), "x"); err != nil || f != nil {
		t.Fatalf("a network that is not viper's is found: %v %v", f, err)
	}
	if _, _, err := c.EnsurePrivateNetwork(context.Background(), "g", ""); err == nil {
		t.Fatal("no Docker host accepted")
	}
}

func TestAServerMemberHasTheAliasTheSubnetAndOnlyTheGroupNetwork(t *testing.T) {
	h := newHost()
	c, dir := newClient(t, h)
	if _, _, err := c.EnsurePrivateNetwork(context.Background(), "g", "default"); err != nil {
		t.Fatal(err)
	}
	srv, err := c.CreateServer(context.Background(), natsSpec("web", "g", "server"))
	if err != nil {
		t.Fatal(err)
	}
	if srv.PrivateIP != "10.9.0.5" || !srv.InNetwork() {
		t.Fatalf("%+v", srv)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "compose", "web", "compose.yaml"))
	for _, want := range []string{"VIPER_NATS_SERVER: \"1\"", "VIPER_NATS_SUBNET: 10.9.0.0/16", "aliases:", "- viper-nats-g", "external: true"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q in\n%s", want, b)
		}
	}
	cl, err := c.CreateServer(context.Background(), natsSpec("api", "g", "client"))
	if err != nil {
		t.Fatal(err)
	}
	cb, _ := os.ReadFile(filepath.Join(dir, "compose", "api", "compose.yaml"))
	if strings.Contains(string(cb), "VIPER_NATS") || strings.Contains(string(cb), "aliases") || cl.PrivateIP == "" {
		t.Errorf("a client is not a server:\n%s", cb)
	}
	// a group network that is not there is an error before anything is written
	if _, err := c.CreateServer(context.Background(), natsSpec("lost", "nogroup", "client")); err == nil {
		t.Error("a machine for a group without a network was made")
	}
	if _, err := os.Stat(filepath.Join(dir, "compose", "lost", "compose.yaml")); err == nil {
		t.Error("a file was written for it")
	}
}

func TestTheStateIsKeptInVolumesAndChangesReplaceIt(t *testing.T) {
	h := newHost()
	c, _ := newClient(t, h)
	ctx := context.Background()
	_, _, _ = c.EnsurePrivateNetwork(ctx, "g", "default")
	srv, err := c.CreateServer(ctx, natsSpec("web", "g", "server"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := c.NATSMetadataOf(ctx, srv.ID)
	if err != nil || m == nil || m.State != model.NATSProvisioning || m.Role != model.NATSServer {
		t.Fatalf("%+v %v", m, err)
	}
	for _, st := range []model.NATSState{model.NATSProvisioning, model.NATSReady} {
		if err := c.UpdateNATSMetadata(ctx, srv.ID, model.NATSMetadata{Group: "g", Role: model.NATSServer, State: st}); err != nil {
			t.Fatal(err)
		}
	}
	var states []string
	for n := range h.volumes {
		if strings.HasPrefix(n, "viper-nats-g_web_") {
			states = append(states, n)
		}
	}
	if len(states) != 1 || states[0] != "viper-nats-g_web_ready" {
		t.Fatalf("state volumes %v", states)
	}
	list, err := c.ListGroupMachines(ctx, "g")
	if err != nil || len(list) != 1 || list[0].NATS.State != model.NATSReady || list[0].Server.Name != "web" || list[0].Server.PrivateIP == "" {
		t.Fatalf("%+v %v", list, err)
	}
	// a change cut off between the new volume and the removal of the old one: the later state wins
	h.volumes["viper-nats-g_web_provisioning"] = map[string]string{"viper-managed": "true", "viper-server": "web", "viper-nats-state": "provisioning"}
	if m, _ := c.NATSMetadataOf(ctx, srv.ID); m == nil || m.State != model.NATSReady {
		t.Fatalf("%+v", m)
	}
	// a machine of no group has no metadata and is no member
	plain, _ := c.CreateServer(ctx, spec("plain"))
	if m, err := c.NATSMetadataOf(ctx, plain.ID); err != nil || m != nil {
		t.Fatalf("%+v %v", m, err)
	}
	if list, _ := c.ListGroupMachines(ctx, "g"); len(list) != 1 {
		t.Fatalf("%+v", list)
	}
}

func TestTheNetworkGoesWhenTheLastMemberIsGone(t *testing.T) {
	h := newHost()
	c, _ := newClient(t, h)
	ctx := context.Background()
	_, _, _ = c.EnsurePrivateNetwork(ctx, "g", "default")
	srv, err := c.CreateServer(ctx, natsSpec("web", "g", "server"))
	if err != nil {
		t.Fatal(err)
	}
	_ = c.UpdateNATSMetadata(ctx, srv.ID, model.NATSMetadata{Group: "g", Role: model.NATSServer, State: model.NATSReady})
	if err := c.DeletePrivateNetwork(ctx, "viper-nats-g"); !errors.Is(err, cloud.ErrNetworkInUse) {
		t.Fatalf("%v", err)
	}
	if err := c.DeleteServer(ctx, srv.ID); err != nil {
		t.Fatal(err)
	}
	for n := range h.volumes {
		t.Errorf("volume left: %s", n) // the state volumes go with the machine
	}
	if err := c.DeletePrivateNetwork(ctx, "viper-nats-g"); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.nets["viper-nats-g"]; ok {
		t.Fatal("the network is still there")
	}
	if err := c.DeletePrivateNetwork(ctx, "viper-nats-g"); err != nil {
		t.Fatalf("a network that is gone is an error: %v", err)
	}
	h.nets["viper-nats-z"] = map[string]string{}
	if err := c.DeletePrivateNetwork(ctx, "viper-nats-z"); !errors.Is(err, compose.ErrNotOurs) {
		t.Fatalf("%v", err)
	}
}

func TestAMachineCannotJoinAGroupLater(t *testing.T) {
	c, _ := newClient(t, newHost())
	if _, err := c.AttachPrivateNetwork(context.Background(), "id", "viper-nats-g"); err == nil {
		t.Fatal("attached")
	}
}

// --- what the review found

func TestDockerHostInTheEnvironmentIsRefused(t *testing.T) {
	t.Setenv("DOCKER_HOST", "ssh://prod")
	h := newHost()
	c, _ := newClient(t, h)
	if _, err := c.CreateServer(context.Background(), spec("web")); err == nil || !strings.Contains(err.Error(), "DOCKER_HOST is set") {
		t.Fatalf("%v", err)
	}
	if f := c.Validate(context.Background(), model.Check{Spec: spec("web"), ServerName: "web"}); len(f) == 0 || f[0].Level != model.Fail {
		t.Fatalf("%v", f)
	}
	if len(h.calls) != 0 {
		t.Fatalf("docker was called: %v", h.calls)
	}
}

func TestNamesThatEndInDockerAreRefused(t *testing.T) {
	c, _ := newClient(t, newHost())
	if _, err := c.ServerByName(context.Background(), "web-docker"); err == nil {
		t.Fatal("a name of a Docker service is accepted")
	}
	if _, err := c.CreateServer(context.Background(), spec("web-docker")); err == nil {
		t.Fatal("a machine named like a Docker service is made")
	}
}

func TestOnlyTheMachineWithThatNameIsFound(t *testing.T) {
	h := newHost()
	c, _ := newClient(t, h)
	srv, err := c.CreateServer(context.Background(), spec("web"))
	if err != nil {
		t.Fatal(err)
	}
	_ = srv
	// the start of an ID is no name
	h.containers["web"].id = "beef01aa22"
	if s, err := c.ServerByName(context.Background(), "beef01"); err != nil || s != nil {
		t.Fatalf("%v %v", s, err)
	}
	h.containers["web"].id = "id-web"
	// the container of the Docker service is no machine, and cannot be deleted as one
	if err := c.DeleteServer(context.Background(), "id-web-docker"); !errors.Is(err, compose.ErrNotOurs) {
		t.Fatalf("%v", err)
	}
	if h.containers["web"] == nil {
		t.Fatal("the machine was deleted through its Docker service")
	}
}

func TestOnlyAnAnswerAboutTheObjectMeansItIsNotThere(t *testing.T) {
	h := newHost()
	c, _ := newClient(t, h)
	broken := func(_ context.Context, _, _ string, _ ...string) (string, string, error) {
		return "", `context "x": context not found: open /home/u/.docker/contexts/meta/ab/meta.json: no such file or directory`, errors.New("exit status 1")
	}
	cb := compose.New(compose.Options{Context: "x", Image: "i", StateDir: t.TempDir()}, broken)
	if s, err := cb.ServerByName(context.Background(), "web"); err == nil || s != nil {
		t.Fatalf("a broken context says the machine is not there: %v %v", s, err)
	}
	if err := cb.DeleteServer(context.Background(), "web"); err == nil {
		t.Fatal("a broken context deleted nothing and said it was fine")
	}
	_ = c
}

func TestTheDockerServiceLeavesNoAnonymousVolume(t *testing.T) {
	h := newHost()
	c, _ := newClient(t, h)
	srv, err := c.CreateServer(context.Background(), spec("web"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := h.volumes["anon-web-docker"]; !ok {
		t.Fatal("the test does not make the volume")
	}
	if err := c.DeleteServer(context.Background(), srv.ID); err != nil {
		t.Fatal(err)
	}
	if len(h.volumes) != 0 {
		t.Fatalf("left: %v", h.volumes)
	}
	// and the sidecar of a machine that is gone
	if _, err := c.CreateServer(context.Background(), spec("web")); err != nil {
		t.Fatal(err)
	}
	delete(h.containers, "web")
	if err := c.DeleteLeftovers(context.Background(), "web"); err != nil {
		t.Fatal(err)
	}
	if len(h.volumes) != 0 {
		t.Fatalf("left after the leftovers: %v", h.volumes)
	}
}

func TestAMarkOfAHalfDoneDownDoesNotSurviveANewStart(t *testing.T) {
	h := newHost()
	c, dir := newClient(t, h)
	if _, err := c.CreateServer(context.Background(), spec("web")); err != nil {
		t.Fatal(err)
	}
	mark := filepath.Join(dir, "compose", "web", "deleting")
	if err := os.WriteFile(mark, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureRunning(context.Background(), "web"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("the mark is still there: a later removal of the container by hand would take the data with it")
	}
}

func TestAStoppedDockerServiceOfARunningMachineIsStarted(t *testing.T) {
	h := newHost()
	c, _ := newClient(t, h)
	if _, err := c.CreateServer(context.Background(), spec("web")); err != nil {
		t.Fatal(err)
	}
	h.containers["web-docker"].run = false
	if err := c.EnsureRunning(context.Background(), "web"); err != nil {
		t.Fatal(err)
	}
	if !h.containers["web-docker"].run || h.called("start web-docker") != 1 {
		t.Fatalf("%v", h.calls)
	}
}

func TestStateVolumesOfDifferentGroupsAndMachinesNeverShareAName(t *testing.T) {
	h := newHost()
	c, _ := newClient(t, h)
	ctx := context.Background()
	for _, g := range []string{"a", "a-b"} {
		_, _, _ = c.EnsurePrivateNetwork(ctx, g, "default")
	}
	s1, _ := c.CreateServer(ctx, natsSpec("b-c", "a", "client"))
	s2, _ := c.CreateServer(ctx, natsSpec("c", "a-b", "client"))
	for _, x := range []struct {
		srv   *model.Server
		group string
	}{{s1, "a"}, {s2, "a-b"}} {
		if err := c.UpdateNATSMetadata(ctx, x.srv.ID, model.NATSMetadata{Group: x.group, Role: model.NATSClient, State: model.NATSReady}); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	for name := range h.volumes {
		if strings.HasSuffix(name, "_ready") {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("two machines, %d state volumes: %v", n, h.volumes)
	}
}

func TestAStaleStateOfAMachineThatWasCutOffDoesNotPassToANewOne(t *testing.T) {
	h := newHost()
	c, _ := newClient(t, h)
	ctx := context.Background()
	_, _, _ = c.EnsurePrivateNetwork(ctx, "g", "default")
	srv, err := c.CreateServer(ctx, natsSpec("web", "g", "client"))
	if err != nil {
		t.Fatal(err)
	}
	_ = c.UpdateNATSMetadata(ctx, srv.ID, model.NATSMetadata{Group: "g", Role: model.NATSClient, State: model.NATSReady})
	h.volumes["web-home"]["mark"] = "data"
	delete(h.containers, "web") // the container is gone, nobody asked for the data to go
	delete(h.containers, "web-docker")
	srv, err = c.CreateServer(ctx, natsSpec("web", "g", "client"))
	if err != nil {
		t.Fatal(err)
	}
	if m, err := c.NATSMetadataOf(ctx, srv.ID); err != nil || m == nil || m.State != model.NATSProvisioning {
		t.Fatalf("the new machine took the old state: %+v %v", m, err)
	}
	if h.volumes["web-home"]["mark"] != "data" {
		t.Fatal("the data volume went with the stale state")
	}
}

func TestAnImageWithARegistryInItsNameIsPulledWhenTheHostLacksIt(t *testing.T) {
	h := newHost()
	h.noImage = true
	c, _ := newClient(t, h, func(o *compose.Options) { o.Image = "ghcr.io/pt9912/viper-machine:v1" })
	var out strings.Builder
	if err := c.PrepareImage(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	if h.called("pull -- ghcr.io/pt9912/viper-machine:v1") != 1 || !strings.Contains(out.String(), "pulling it") {
		t.Fatalf("%v\n%s", h.calls, out.String())
	}
	// it is there now: nothing more
	n := len(h.calls)
	if err := c.PrepareImage(context.Background(), &out); err != nil || h.called("pull") != 1 || len(h.calls) != n+1 {
		t.Errorf("a second call pulled again: %v %v", err, h.calls)
	}
	// the preflight does not fail for it
	h.noImage = true
	f := c.Validate(context.Background(), model.Check{Spec: spec("web"), ServerName: "web"})
	for _, x := range f {
		if x.Level == model.Fail && strings.Contains(x.Text, "image") {
			t.Errorf("an image that can be pulled is a failure: %v", x)
		}
	}
}

func TestAnImageBuiltHereIsNotPulledAndTheErrorSaysHowToMakeIt(t *testing.T) {
	h := newHost()
	h.noImage = true
	c, _ := newClient(t, h) // viper-machine:test: no registry in the name
	err := c.PrepareImage(context.Background(), &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "viper image build") || h.called("pull") != 0 {
		t.Fatalf("%v %v", err, h.calls)
	}
}
