package app

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/aws"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/azure"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/compose"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/gcp"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/hetzner"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/kubernetes"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/system"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

// Provider is the adapter chosen for a machine; its Provider is nil if the name has no adapter.
type Provider struct{ cloud.Provider }

// Unwrap is the adapter inside (cloud.AsNATS looks through it for capabilities the interface does not show).
func (p Provider) Unwrap() any {
	if p.Provider == nil {
		return nil // an empty wrapper holds nothing (a nil interface inside any is not nil)
	}
	return p.Provider
}

// NewProvider is the adapter of the provider of the machine (one of model.ProviderNames). A
// provider reads its own settings (token, region, project, ...) from the machine's settings.
func NewProvider(m *model.Machine) Provider {
	switch m.Provider {
	case "hetzner":
		return Provider{hetzner.New(m.Get("HCLOUD_TOKEN"), "")}
	case "azure":
		root, _ := strconv.Atoi(m.Get("AZURE_ROOT_GB"))
		return Provider{azure.New(azure.Options{
			SubscriptionID: m.Get("AZURE_SUBSCRIPTION_ID"), ResourceGroup: m.Get("AZURE_RESOURCE_GROUP"), Location: m.ServerLocation,
			SubnetID: m.Get("AZURE_SUBNET_ID"), RootGB: int32(root), NATSPort: m.NATS.Port,
		})}
	case "kubernetes":
		return Provider{kubernetes.New(kubernetesOptions(m))}
	case "compose":
		return Provider{compose.New(composeOptions(m), system.Run)}
	case "gcp":
		root, _ := strconv.Atoi(m.Get("GCP_ROOT_GB"))
		return Provider{gcp.New(gcp.Options{Project: m.Get("GCP_PROJECT"), Zone: m.ServerLocation, Network: m.Get("GCP_NETWORK"), Subnet: m.Get("GCP_SUBNET"), RootGB: int64(root), NATSPort: m.NATS.Port})}
	case "aws":
		root, _ := strconv.Atoi(m.Get("AWS_ROOT_GB"))
		return Provider{aws.New(aws.Options{
			Location: m.ServerLocation, Profile: m.Get("AWS_PROFILE"), SubnetID: m.Get("AWS_SUBNET_ID"),
			VPCID: m.Get("AWS_VPC_ID"), RootGB: int32(root), Tags: m.Get("AWS_TAGS"), SharedTags: m.Get("AWS_TAGS_SHARED"), NATSPort: m.NATS.Port,
		})}
	}
	return Provider{}
}

// providerOf is the adapter of the machine's provider. settingsfile.Load has checked the name; a name
// without an adapter (the lists drifted apart) gives a provider that fails with that reason
// instead of a nil that panics.
func providerOf(c *model.Machine) Provider {
	if p := NewProvider(c); p.Provider != nil {
		return p
	}
	return Provider{noAdapter{name: c.Provider}}
}

// noAdapter is the provider of a name that has no adapter.
type noAdapter struct{ name string }

func (n noAdapter) err() error              { return fmt.Errorf("no adapter for provider %s", n.name) }
func (n noAdapter) Name() string            { return n.name }
func (n noAdapter) Title() string           { return n.name }
func (noAdapter) Describe(err error) string { return err.Error() }
func (noAdapter) Hint() string              { return "PROVIDER" }

func (n noAdapter) Validate(context.Context, model.Check) []model.Finding {
	return []model.Finding{{Level: model.Fail, Text: n.err().Error()}}
}

func (n noAdapter) ServerByName(context.Context, string) (*model.Server, error) { return nil, n.err() }

func (n noAdapter) CreateServer(context.Context, model.ServerSpec) (*model.Server, error) {
	return nil, n.err()
}

func (n noAdapter) DeleteServer(context.Context, string) error { return n.err() }

func (n noAdapter) EnsureLoginKey(context.Context, string, string) (model.LoginKey, error) {
	return model.LoginKey{}, n.err()
}

func (n noAdapter) EnsureFirewall(context.Context, string, []string) (model.FirewallResult, error) {
	return model.FirewallResult{}, n.err()
}

// kubernetesOptions are the settings of a machine in a Kubernetes cluster.
func kubernetesOptions(m *model.Machine) kubernetes.Options {
	disk, _ := strconv.Atoi(m.Get("K8S_DISK_GB"))
	brew, _ := strconv.Atoi(m.Get("K8S_BREW_GB"))
	return kubernetes.Options{BrewGB: brew, PullSecret: m.Get("K8S_IMAGE_PULL_SECRET"),
		Context: m.ServerLocation, Namespace: m.Get("K8S_NAMESPACE"), Image: m.ServerImage, StorageClass: m.Get("K8S_STORAGE_CLASS"),
		DiskGB: disk, Docker: m.Get("K8S_DOCKER"), DockerHost: m.Get("K8S_DOCKER_HOST"), DockerImage: m.Get("K8S_DOCKER_IMAGE"), NATSPort: m.NATS.Port,
		K3DRegistry: m.Get("K3D_REGISTRY"), K3DCreateCluster: m.Get("K3D_CLUSTER_CREATE") == "1", K3DAgents: atoi(m.Get("K3D_AGENTS")),
		StateDir: m.StateDir, Run: system.Run,
	}
}

// composeOptions are the settings of a machine on a Docker host.
func composeOptions(m *model.Machine) compose.Options {
	return compose.Options{Context: m.ServerLocation, Image: m.ServerImage, StateDir: m.StateDir,
		Docker: m.Get("COMPOSE_DOCKER"), DockerExplicit: m.Get("COMPOSE_DOCKER") != "",
		DockerHost: m.Get("COMPOSE_DOCKER_HOST"), DockerImage: m.Get("COMPOSE_DOCKER_IMAGE"), NATSPort: m.NATS.Port}
}

// Proxy connects stdin and stdout to the ssh port of a container (viper k8s-proxy, viper docker-proxy).
func Proxy(ctx context.Context, m *model.Machine, in io.Reader, out io.Writer) error {
	if m.Provider == "compose" {
		return compose.New(composeOptions(m), system.Run).Proxy(ctx, m.ServerName, in, out)
	}
	return kubernetes.New(kubernetesOptions(m)).Proxy(ctx, m.ServerName, in, out)
}

// atoi reads a setting that settingsfile has checked; a value that is no number counts as 0.
func atoi(v string) int {
	n, _ := strconv.Atoi(v)
	return n
}
