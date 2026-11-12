package compose

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/pt9912/hetzner-viper/internal/model"
)

// The compose file of a machine. The types are written out (not built as maps of any) so that the file has the same
// fields in the same order every time: viper up on a machine that exists writes it again, and a change would mean a new
// container (docs/plan/compose.md §7.10).
type composeFile struct {
	Name     string             `yaml:"name"`
	Services map[string]service `yaml:"services"`
	Volumes  map[string]volume  `yaml:"volumes"`
	Networks map[string]network `yaml:"networks,omitempty"`
	Secrets  map[string]secret  `yaml:"secrets"`
}

type service struct {
	Image         string               `yaml:"image"`
	PullPolicy    string               `yaml:"pull_policy"`
	ContainerName string               `yaml:"container_name"`
	Privileged    bool                 `yaml:"privileged,omitempty"`
	NetworkMode   string               `yaml:"network_mode,omitempty"`
	Command       []string             `yaml:"command,omitempty"`
	Environment   map[string]string    `yaml:"environment,omitempty"`
	CPUs          float64              `yaml:"cpus,omitempty"`
	MemLimit      string               `yaml:"mem_limit,omitempty"`
	Volumes       []string             `yaml:"volumes,omitempty"`
	Secrets       []secretRef          `yaml:"secrets,omitempty"`
	Networks      map[string]netAttach `yaml:"networks,omitempty"`
	Healthcheck   *healthcheck         `yaml:"healthcheck,omitempty"`
	DependsOn     map[string]dependsOn `yaml:"depends_on,omitempty"`
	Restart       string               `yaml:"restart"`
	Labels        map[string]string    `yaml:"labels"`
}

type secretRef struct {
	Source string `yaml:"source"`
	Target string `yaml:"target"`
}

// netAttach is how a service is on a network: with the names it can be found by there.
type netAttach struct {
	Aliases []string `yaml:"aliases,omitempty"`
}

type healthcheck struct {
	Test        []string `yaml:"test"`
	Interval    string   `yaml:"interval"`
	Timeout     string   `yaml:"timeout"`
	Retries     int      `yaml:"retries"`
	StartPeriod string   `yaml:"start_period,omitempty"`
}

type dependsOn struct {
	Condition string `yaml:"condition"`
	Restart   bool   `yaml:"restart,omitempty"`
}

type volume struct {
	Name   string            `yaml:"name"`
	Labels map[string]string `yaml:"labels"`
}

type network struct {
	External bool   `yaml:"external"`
	Name     string `yaml:"name"`
}

type secret struct {
	File string `yaml:"file"`
}

// secretFiles are the keys of the machine, by the name they have on the host and in the container.
func secretFiles() []string {
	return []string{"authorized_keys", "host_key", "host_key.pub", "host_key-cert.pub"}
}

const groupNet = "group"

func labelsOf(server string, extra map[string]string) map[string]string {
	l := map[string]string{labelManaged: "true", labelServer: server}
	for k, v := range extra {
		l[k] = v
	}
	return l
}

func (o Options) dockerEnv() map[string]string {
	switch o.docker() {
	case DockerDind:
		return map[string]string{"DOCKER_HOST": fmt.Sprintf("tcp://127.0.0.1:%d", DockerPort)}
	case DockerRemote:
		return map[string]string{"DOCKER_HOST": o.DockerHost}
	}
	return nil
}

// fileFor is the compose file of the machine. The keys are files next to it (./secret/), bind-mounted to the place the
// image reads them from. s.Network, if set, is the group network of a NATS group (made by viper, not by compose): the
// machine is then on that network only.
func (o Options) fileFor(s model.ServerSpec, cpus float64, memMiB int, subnet string) composeFile {
	name := s.Name
	m := service{
		Image: o.Image, PullPolicy: "never", ContainerName: name,
		Environment: o.dockerEnv(),
		CPUs:        cpus, MemLimit: fmt.Sprintf("%dm", memMiB),
		Volumes: []string{"home:/home/builder", "brew:/home/linuxbrew"},
		Healthcheck: &healthcheck{Test: []string{"CMD", "bash", "-c", fmt.Sprintf("exec 3<>/dev/tcp/127.0.0.1/%d", SSHPort)},
			Interval: "2s", Timeout: "3s", Retries: 3, StartPeriod: "10m"}, // the first start puts Homebrew on its volume
		Restart: "unless-stopped",
		Labels:  labelsOf(name, s.Labels),
	}
	f := composeFile{
		Name:     projectName(name),
		Services: map[string]service{serviceMachine: m},
		Volumes: map[string]volume{
			"home": {Name: homeVolume(name), Labels: labelsOf(name, nil)},
			"brew": {Name: brewVolume(name), Labels: labelsOf(name, nil)},
		},
		Secrets: map[string]secret{},
	}
	for _, sf := range secretFiles() {
		f.Secrets[sf] = secret{File: "./secret/" + sf}
		m.Secrets = append(m.Secrets, secretRef{Source: sf, Target: secretDir + "/" + sf})
	}
	if s.Network != "" {
		f.Networks = map[string]network{groupNet: {External: true, Name: s.Network}}
		attach := netAttach{}
		if s.Labels[model.LabelNATSRole] == string(model.NATSServer) {
			// the server is found by the name of the group, which its certificate is for; it listens on its address in the
			// network (the entrypoint finds it from the subnet), and runs nats-server as a process of the container
			attach.Aliases = []string{s.Network}
			m.Environment = withEnv(m.Environment, "VIPER_NATS_SERVER", "1")
			m.Environment = withEnv(m.Environment, "VIPER_NATS_SUBNET", subnet)
		}
		m.Networks = map[string]netAttach{groupNet: attach}
	}
	if o.docker() == DockerDind {
		f.Services[serviceDocker] = o.dockerService(name)
	}
	f.Services[serviceMachine] = m
	return f
}

// dockerService is the Docker daemon next to the machine (COMPOSE_DOCKER=dind). It shares the network of the machine and
// listens on its loopback address only: without TLS and without a login, anything that reached it would be root on the
// host. It is privileged, which a Docker daemon needs. restart: true restarts it with the machine on compose operations
// (a plain docker restart of the machine is repaired by viper: docs/plan/compose.md §7.8).
func (o Options) dockerService(server string) service {
	addr := fmt.Sprintf("tcp://127.0.0.1:%d", DockerPort)
	return service{
		Image: o.dockerImage(), PullPolicy: "missing", ContainerName: dockerName(server),
		Privileged: true, NetworkMode: "service:" + serviceMachine,
		Environment: map[string]string{"DOCKER_TLS_CERTDIR": ""},
		Command:     []string{"dockerd", "--host=unix:///var/run/docker.sock", "--host=" + addr},
		Healthcheck: &healthcheck{Test: []string{"CMD", "docker", "-H", addr, "info"}, Interval: "3s", Timeout: "5s", Retries: 20},
		DependsOn:   map[string]dependsOn{serviceMachine: {Condition: "service_started", Restart: true}},
		Restart:     "unless-stopped",
		Labels:      labelsOf(server, nil),
	}
}

func (f composeFile) marshal() ([]byte, error) { return yaml.Marshal(f) }

func withEnv(env map[string]string, k, v string) map[string]string {
	if env == nil {
		env = map[string]string{}
	}
	env[k] = v
	return env
}
