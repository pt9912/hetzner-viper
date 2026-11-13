// Package settings is the driven port to the settings of the machines: the files .env.local and
// machines/<machine>.env.local, read together with what the environment gives. An adapter
// (internal/adapter/driven/settingsfile) reads them.
package settings

import "github.com/pt9912/hetzner-viper/internal/model"

// Env is what the settings depend on besides the files.
type Env struct {
	Lookup func(string) (string, bool) // os.LookupEnv
	Home   string
	// Version is the version of viper ("v0.18.0"; "dev" or "v0.18.0-3-gabc" for a build that is not a release). The machine
	// image of a release is published under it, which makes it the default of K8S_IMAGE and COMPOSE_IMAGE.
	Version string
	// Group is the group a machine is looked at in ("" if the settings find it). A machine may be named in several groups;
	// then the group says whose settings apply (viper up web --group dev). It must name the machine.
	Group string
}

// Getenv is the value of an environment variable, "" if it is not set.
func (e Env) Getenv(name string) string {
	if e.Lookup == nil {
		return ""
	}
	v, _ := e.Lookup(name)
	return v
}

// Store reads the settings.
type Store interface {
	// Load reads the settings of a machine and checks them. With needTokens it requires the
	// credentials of the cloud and of Cloudflare as well (all commands that call an API).
	Load(dir, machine string, env Env, needTokens bool) (*model.Machine, error)
	// LoadFile reads .env.local of dir (without a machine).
	LoadFile(dir string, env Env) (map[string]string, error)
	// LoadGroup reads groups/<name>.env of dir: the machines it names and its settings, checked.
	LoadGroup(dir, name string, env Env) (*model.Group, error)
	// Prefix is SERVER_PREFIX of a machine, as far as the files tell it.
	Prefix(dir, machine string, env Env) (string, error)
	// ProviderOf is the cloud provider and DNSProviderOf the DNS provider the settings in vars name.
	ProviderOf(vars map[string]string) string
	DNSProviderOf(vars map[string]string) string
}
