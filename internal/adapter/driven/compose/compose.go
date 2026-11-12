// Package compose is the adapter of the cloud port (internal/port/driven/cloud) for a Docker host
// (docs/plan/compose.md): a "server" is a container of the image viper-machine (image/) that viper starts with
// docker compose, from a compose.yaml it writes itself, one project per machine. The container runs sshd as builder on
// port 2222, without root; viper reaches it with docker exec (viper docker-proxy), not through an address. The Docker
// host is the context named by SERVER_LOCATION; version 1 supports a local daemon only, because the keys are
// bind-mounted files (docs/plan/compose.md §7.7).
package compose

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/system"
)

// The names and ports the adapter and the image agree on.
const (
	// SSHPort is where sshd of the image listens.
	SSHPort = 2222
	// DockerPort is where the Docker daemon of the sidecar listens (in the network of the machine only).
	DockerPort = 2375
	// DefaultSize is SERVER_TYPE when it is not set: CPU x GiB of memory.
	DefaultSize = "2x4"

	secretDir = "/run/viper/secret"
	// serviceMachine and serviceDocker are the services of the compose file.
	serviceMachine = "machine"
	serviceDocker  = "docker"
)

// Docker modes (COMPOSE_DOCKER).
const (
	DockerDind   = "dind"
	DockerRemote = "remote"
	DockerNone   = "none"
)

// Labels viper puts on what it makes.
const (
	labelManaged = model.LabelManaged
	labelServer  = "viper-server"
)

// Options are the settings of the adapter.
type Options struct {
	Context  string // SERVER_LOCATION: the Docker context ("default" is the local daemon)
	Image    string // COMPOSE_IMAGE
	StateDir string // where the compose files and the keys of the machines are kept
	Docker   string // COMPOSE_DOCKER: dind, remote or none
	// DockerExplicit is whether COMPOSE_DOCKER was set by the user (the privileged dind service is only taken on purpose).
	DockerExplicit bool
	DockerHost     string // COMPOSE_DOCKER_HOST: where a remote daemon is
	DockerImage    string // COMPOSE_DOCKER_IMAGE: the image of the daemon of the sidecar
	NATSPort       int
	// Timeout is how long docker compose up waits for the machine to be healthy (default 10 minutes: the first start
	// puts Homebrew on its volume).
	Timeout time.Duration
}

func (o Options) docker() string {
	if o.Docker != "" {
		return o.Docker
	}
	return DockerDind
}

func (o Options) dockerImage() string {
	if o.DockerImage != "" {
		return o.DockerImage
	}
	return "docker:dind"
}

func (o Options) timeout() time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return 10 * time.Minute
}

// Client talks to the Docker host through the docker program.
type Client struct {
	opts Options
	run  system.Runner
}

// New returns a client that runs docker through run.
func New(opts Options, run system.Runner) *Client { return &Client{opts: opts, run: run} }

// Name is the short name of the provider in messages.
func (*Client) Name() string { return "Docker Compose" }

// Title is the name of the provider in the preflight.
func (*Client) Title() string { return "Docker Compose" }

// Hint says what to check when docker cannot be used.
func (*Client) Hint() string {
	return "docker is installed and running / SERVER_LOCATION (the Docker context) / the docker compose plugin / your rights on the Docker socket"
}

// Describe is a docker error without more than its message, safe to show.
func (*Client) Describe(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

// ErrNotOurs is returned for an object of the name that viper did not make.
var ErrNotOurs = errors.New("an object of that name exists that viper did not make")

var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// checkName: names go into container, volume and project names and into file paths.
func checkName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("the name %q is not usable for a container: lowercase letters, digits and hyphens", name)
	}
	if strings.HasSuffix(name, "-docker") { // <name>-docker is the container of the Docker service of the machine <name>
		return fmt.Errorf("the name %q is not usable: it ends in -docker, which names the Docker service of a machine", name)
	}
	return nil
}

func projectName(server string) string { return "viper-" + server }
func homeVolume(server string) string  { return server + "-home" }
func brewVolume(server string) string  { return server + "-brew" }
func dockerName(server string) string  { return server + "-docker" }

// ProjectDir is where the compose file and the keys of the machine are kept.
func (c *Client) projectDir(server string) string {
	return filepath.Join(c.opts.StateDir, "compose", server)
}

func (c *Client) composeFile(server string) string {
	return filepath.Join(c.projectDir(server), "compose.yaml")
}

// docker runs the docker program against the Docker host of the client. The context is given as a value of its own, and
// one that begins with a dash is refused, so that it is never read as an option.
func (c *Client) docker(ctx context.Context, args ...string) (string, error) {
	return c.dockerIn(ctx, "", args...)
}

func (c *Client) dockerIn(ctx context.Context, stdin string, args ...string) (string, error) {
	if c.opts.Context == "" {
		return "", errors.New("SERVER_LOCATION is not set: it is the Docker context (\"default\" is the local daemon; viper does not take the current one, so that nothing is made on the wrong host)")
	}
	if err := c.checkTarget(); err != nil {
		return "", err
	}
	out, errOut, err := c.run(ctx, stdin, "docker", append([]string{"--context", c.opts.Context}, args...)...)
	if err != nil {
		msg := strings.TrimSpace(errOut)
		if msg == "" {
			msg = err.Error()
		}
		return out, &dockerError{args: args, msg: msg, err: err}
	}
	return out, nil
}

// checkTarget refuses a context that could be taken for an option, and a DOCKER_HOST in the environment: docker lets it
// win over --context, so every call would go to that host and not to the one SERVER_LOCATION names.
func (c *Client) checkTarget() error {
	if c.opts.Context == "" || strings.HasPrefix(c.opts.Context, "-") {
		return fmt.Errorf("SERVER_LOCATION: %q is not a Docker context", c.opts.Context)
	}
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		return fmt.Errorf("DOCKER_HOST is set (%s): docker uses it instead of the Docker context %s of SERVER_LOCATION; unset it for viper", h, c.opts.Context)
	}
	return nil
}

// dockerError is a failed docker call, worded with what docker said.
type dockerError struct {
	args []string
	msg  string
	err  error
}

func (e *dockerError) Error() string {
	verb := ""
	if len(e.args) > 0 {
		verb = " " + e.args[0]
		if e.args[0] == "compose" && len(e.args) > 1 {
			verb = " compose"
		}
	}
	return fmt.Sprintf("docker%s: %s", verb, e.msg)
}
func (e *dockerError) Unwrap() error { return e.err }

// notFound says whether docker reported that the object is not there.
func notFound(err error) bool {
	var de *dockerError
	if !errors.As(err, &de) {
		return false
	}
	// not just "no such": a broken context says "no such file or directory", which is not an answer about the object
	msg := strings.ToLower(de.msg)
	for _, w := range []string{"no such container", "no such volume", "no such network", "no such object"} {
		if strings.Contains(msg, w) {
			return true
		}
	}
	return false
}

// compose runs docker compose for the project of the server.
func (c *Client) compose(ctx context.Context, server string, args ...string) (string, error) {
	return c.docker(ctx, append([]string{"compose", "-p", projectName(server), "-f", c.composeFile(server)}, args...)...)
}
