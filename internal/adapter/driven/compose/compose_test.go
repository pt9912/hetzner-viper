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
	noImage    bool
	networks   int
}

type ctr struct {
	id     string
	labels map[string]string
	run    bool
}

func newHost() *host {
	return &host{containers: map[string]*ctr{}, volumes: map[string]map[string]string{}, noEth0: map[string]bool{}, endpoint: "unix:///var/run/docker.sock"}
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
	case args[0] == "image":
		if h.noImage {
			return nosuch("image", args[len(args)-1])
		}
		return "sha256:1\n", "", nil
	case args[0] == "network":
		return strings.Repeat("abc\n", h.networks), "", nil
	case args[0] == "container" && args[1] == "inspect" && args[2] == "--format":
		return "healthy\n", "", nil
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

func (h *host) inspect(ref string) (string, string, error) {
	for n, c := range h.containers {
		if n == ref || c.id == ref {
			b, _ := json.Marshal([]map[string]any{{
				"Id": c.id, "Created": time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC),
				"Config": map[string]any{"Labels": c.labels}, "State": map[string]any{"Running": c.run},
			}})
			return string(b), "", nil
		}
	}
	return nosuch("container", ref)
}

type fileSvc struct {
	ContainerName string            `yaml:"container_name"`
	Labels        map[string]string `yaml:"labels"`
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
			h.containers[s.ContainerName] = &ctr{id: "id-" + s.ContainerName, labels: s.Labels, run: true}
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
	if h.containers["viper-web-docker"] == nil || len(h.volumes) != 2 {
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

func isWindows() bool { return os.PathSeparator == '\\' }
