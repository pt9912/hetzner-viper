// Package kubernetes is the adapter of the cloud port (internal/port/driven/cloud) for a Kubernetes cluster
// (docs/plan/kubernetes.md): a "server" is a StatefulSet with one pod, made from the image viper-machine (image/),
// with a Secret for its keys and a PersistentVolumeClaim for its home directory. The pod runs sshd as builder
// on port 2222, without root; viper reaches it through the API of the cluster (viper k8s-proxy), not through an
// address. The cluster is the context of the kubeconfig.
package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/system"
)

// The names and ports the adapter and the image agree on.
const (
	// SSHPort is where sshd of the image listens.
	SSHPort = 2222
	// DockerPort is where the Docker daemon of the sidecar listens (inside the pod only).
	DockerPort = 2375
	// DefaultSize is SERVER_TYPE when it is not set: CPU x GiB of memory.
	DefaultSize = "2x4"

	machineContainer = "machine"
	dockerContainer  = "dockerd"
	secretDir        = "/run/viper/secret"
	homeDir          = "/home/builder"
	uid              = 1000
)

// Docker modes (K8S_DOCKER).
const (
	DockerDind   = "dind"
	DockerRemote = "remote"
	DockerNone   = "none"
)

// Options are the settings of the adapter.
type Options struct {
	Context      string // SERVER_LOCATION: the context of the kubeconfig
	Namespace    string // K8S_NAMESPACE
	Image        string // K8S_IMAGE
	StorageClass string // K8S_STORAGE_CLASS, "" for the default of the cluster
	DiskGB       int    // K8S_DISK_GB
	BrewGB       int    // K8S_BREW_GB: the volume of Homebrew
	// PullSecret is K8S_IMAGE_PULL_SECRET: the name of a Secret of type docker-registry in the namespace, for a registry
	// that wants a login. The user makes it; viper only names it in the pod and never changes or deletes it.
	PullSecret  string
	Docker      string // K8S_DOCKER: dind, remote or none
	DockerHost  string // K8S_DOCKER_HOST: where a remote daemon is
	DockerImage string // K8S_DOCKER_IMAGE: the image of the daemon of the sidecar
	NATSPort    int
	// K3DRegistry is K3D_REGISTRY: the name of a k3d registry that viper up makes (if it is missing) and puts the image
	// into, so that the nodes of a k3d cluster pull it from there. Only for a context named k3d-<cluster>.
	K3DRegistry string
	// K3DCreateCluster is K3D_CLUSTER_CREATE: viper up makes the k3d cluster too, with that registry, if it is missing.
	K3DCreateCluster bool
	K3DAgents        int // K3D_AGENTS: the agent nodes of a cluster viper makes
	// StateDir is where the lock of the registry is kept. Run runs docker and k3d.
	StateDir string
	Run      system.Runner
	// Poll is how long to wait between two looks at an object (default 2 s); Timeout how long to wait for a pod.
	Poll, Timeout time.Duration
}

func (o Options) namespace() string {
	if o.Namespace != "" {
		return o.Namespace
	}
	return "viper"
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

func (o Options) diskGB() int {
	if o.DiskGB > 0 {
		return o.DiskGB
	}
	return 20
}

func (o Options) brewGB() int {
	if o.BrewGB > 0 {
		return o.BrewGB
	}
	return 10
}

func (o Options) poll() time.Duration {
	if o.Poll > 0 {
		return o.Poll
	}
	return 2 * time.Second
}

func (o Options) timeout() time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return 10 * time.Minute
}

// Client talks to the cluster.
type Client struct {
	opts Options

	mu       sync.Mutex
	prepared bool   // PrepareImage has run
	lease    string // the lease file of a machine that is about to be made
	source   string // the image as K8S_IMAGE names it, before PrepareImage replaced it
	cs       kubernetes.Interface
	cfg      *rest.Config // nil for a client made over a given clientset
}

// New returns a client. It reads the kubeconfig when it is first used.
func New(opts Options) *Client { return &Client{opts: opts} }

// NewWith returns a client over a given clientset (for tests).
func NewWith(opts Options, cs kubernetes.Interface) *Client { return &Client{opts: opts, cs: cs} }

// Name is the short name of the provider in messages.
func (*Client) Name() string { return "Kubernetes" }

// Title is the name of the provider in the preflight.
func (*Client) Title() string { return "Kubernetes" }

// Hint says what to check when the API cannot be used.
func (*Client) Hint() string {
	return "kubeconfig / SERVER_LOCATION (the context) / the login of the cluster / network"
}

// Describe is an API error without the details of the request, safe to show.
func (*Client) Describe(err error) string {
	if err == nil {
		return ""
	}
	var st apierrors.APIStatus
	if errors.As(err, &st) {
		s := st.Status()
		return fmt.Sprintf("%d %s: %s", s.Code, s.Reason, s.Message)
	}
	return strings.TrimSpace(err.Error())
}

// session is the clientset of a client once it is made. client returns it instead of the interface itself, which the
// linter (ireturn) does not allow; the methods of kubernetes.Interface are its own.
type session struct{ kubernetes.Interface }

// client makes the clientset when it is first needed.
func (c *Client) client() (*session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cs != nil {
		return &session{c.cs}, nil
	}
	if c.opts.Context == "" {
		return nil, errors.New("SERVER_LOCATION is not set: it is the context of the kubeconfig (viper does not take the current one, so that nothing is made in the wrong cluster)")
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: c.opts.Context}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("kubeconfig, context %s: %w", c.opts.Context, err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	c.cs, c.cfg = cs, cfg
	return &session{cs}, nil
}

// wait calls done until it reports true, looking every Poll, for at most Timeout.
func (c *Client) wait(ctx context.Context, what string, done func() (bool, error)) error {
	deadline := time.Now().Add(c.opts.timeout())
	for {
		ok, err := done()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %s", what)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.opts.poll()):
		}
	}
}

// ErrNotOurs is returned for an object of the name that viper did not make.
var ErrNotOurs = errors.New("an object of that name exists that viper did not make")

// Labels viper puts on what it makes.
const (
	labelManaged = model.LabelManaged
	labelServer  = "viper-server"
)
