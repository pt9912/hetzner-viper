package app

import (
	"context"
	"fmt"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/hetzner"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

// NewProvider is the adapter of the provider named by name (one of config.ProviderNames); nil
// for another name. A provider reads its own settings (token, region, project, ...) through get.
func NewProvider(name string, get func(string) string) cloud.Provider {
	switch name {
	case "hetzner":
		return hetzner.New(get("HCLOUD_TOKEN"), "")
	}
	return nil
}

// providerOf is the adapter of the machine's provider. config.Load has checked the name; a name
// without an adapter (the lists drifted apart) gives a provider that fails with that reason
// instead of a nil that panics.
func providerOf(c *model.Machine) cloud.Provider {
	if p := NewProvider(c.Provider, c.Get); p != nil {
		return p
	}
	return noAdapter{name: c.Provider}
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
