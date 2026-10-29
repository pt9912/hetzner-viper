package app

import (
	"context"
	"fmt"
	"strconv"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/aws"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/azure"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/gcp"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/hetzner"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

// Provider is the adapter chosen for a machine; its Provider is nil if the name has no adapter.
type Provider struct{ cloud.Provider }

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
			SubnetID: m.Get("AZURE_SUBNET_ID"), RootGB: int32(root),
		})}
	case "gcp":
		root, _ := strconv.Atoi(m.Get("GCP_ROOT_GB"))
		return Provider{gcp.New(gcp.Options{Project: m.Get("GCP_PROJECT"), Zone: m.ServerLocation, Network: m.Get("GCP_NETWORK"), Subnet: m.Get("GCP_SUBNET"), RootGB: int64(root)})}
	case "aws":
		root, _ := strconv.Atoi(m.Get("AWS_ROOT_GB"))
		return Provider{aws.New(aws.Options{
			Location: m.ServerLocation, Profile: m.Get("AWS_PROFILE"), SubnetID: m.Get("AWS_SUBNET_ID"),
			VPCID: m.Get("AWS_VPC_ID"), RootGB: int32(root), Tags: m.Get("AWS_TAGS"), SharedTags: m.Get("AWS_TAGS_SHARED"),
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
