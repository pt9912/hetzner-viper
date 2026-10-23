package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	nameRe   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,63}$`)
)

// Env is what the settings depend on besides the files.
type Env struct {
	Lookup func(string) (string, bool) // os.LookupEnv
	Home   string
}

// Getenv is the value of an environment variable, "" if it is not set.
func (e Env) Getenv(name string) string {
	if e.Lookup == nil {
		return ""
	}
	v, _ := e.Lookup(name)
	return v
}

// Config is the settings of one machine with the names and paths derived from them (common.sh).
type Config struct {
	Machine string
	Dir     string // where .env.local is
	vars    map[string]string
	env     Env

	DNSDomain, ServerType, ServerLocation, ServerImage, ServerPrefix, WorkspaceDir string
	SSHKey                                                                         string

	ServerName, SSHAlias, DNSHost, KeyName, FirewallName string
	StateDir, SSHDir, SSHConfig, KnownPin                string

	// state files of the machine (common.sh), all keyed by the server name
	IPState, IDState, WorkspaceState, ReadyState, RepoState string
	EventsLog, LogDir                                       string
}

// Setting is a setting with a default for when it is unset or empty (${NAME:-default}).
func (c *Config) Setting(name, def string) string {
	return or(c.Get(name), def)
}

// Get returns a setting: the files take precedence over the environment, like with `source`.
func (c *Config) Get(name string) string {
	if v, ok := c.vars[name]; ok {
		return v
	}
	return c.env.Getenv(name)
}

// Tokens are the settings only the commands that talk to Hetzner and Cloudflare need.
var tokens = []string{"HCLOUD_TOKEN", "CLOUDFLARE_API_TOKEN", "CLOUDFLARE_ZONE_ID"}

// LoadFile reads .env.local of dir (without a machine).
func LoadFile(dir string, env Env) (map[string]string, error) {
	vars := map[string]string{}
	if err := sourceFile(vars, filepath.Join(dir, ".env.local"), env); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("Copy .env.local.example to .env.local")
		}
		return nil, err
	}
	return vars, nil
}

func sourceFile(vars map[string]string, path string, env Env) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lookup := func(k string) (string, bool) {
		if k == "HOME" {
			return env.Home, true
		}
		if env.Lookup == nil {
			return "", false
		}
		return env.Lookup(k)
	}
	if err := Source(vars, string(b), lookup); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// Load reads .env.local and machines/<machine>.env.local of dir and checks the settings. With
// needTokens it requires the three API credentials as well (all commands that call an API).
func Load(dir, machine string, env Env, needTokens bool) (*Config, error) {
	if machine == "" {
		return nil, errors.New("Machine name required")
	}
	vars, err := LoadFile(dir, env)
	if err != nil {
		return nil, err
	}
	if !nameRe.MatchString(machine) {
		return nil, errors.New("Machine name: lowercase letters, digits, hyphens")
	}
	if err := sourceFile(vars, filepath.Join(dir, "machines", machine+".env.local"), env); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	c := &Config{Machine: machine, Dir: dir, vars: vars, env: env}

	required := []string{"DNS_DOMAIN"}
	if needTokens {
		required = append(required, tokens...)
	}
	var missing []string
	for _, v := range required {
		if c.Get(v) == "" {
			missing = append(missing, v)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("Missing settings: %s (in .env.local or machines/%s.env.local; the three tokens may also be exported in the shell)", strings.Join(missing, " "), machine)
	}
	c.DNSDomain = c.Get("DNS_DOMAIN")
	if err := CheckDomain(c.DNSDomain); err != nil {
		return nil, err
	}
	c.SSHKey = KeyPath(c.Get("SSH_KEY"), dir, env.Home)
	c.ServerType = or(c.Get("SERVER_TYPE"), "cpx32")
	c.ServerLocation = or(c.Get("SERVER_LOCATION"), "fsn1")
	c.ServerImage = or(c.Get("SERVER_IMAGE"), "ubuntu-26.04")
	c.WorkspaceDir = or(c.Get("WORKSPACE_DIR"), "/Development")
	c.ServerPrefix = or(c.Get("SERVER_PREFIX"), "claude")
	if !nameRe.MatchString(c.ServerPrefix) {
		return nil, fmt.Errorf("SERVER_PREFIX: lowercase letters, digits, hyphens: %s", c.ServerPrefix)
	}
	c.ServerName = c.ServerPrefix + "-" + machine
	if len(c.ServerName) > 63 {
		return nil, fmt.Errorf("Server name %s is longer than 63 characters", c.ServerName)
	}
	// Everything per machine is keyed by the server name (prefix + machine), so equal machine
	// names under different prefixes never share a DNS record or local files.
	c.SSHAlias = "hetzner-" + c.ServerName
	c.DNSHost = c.ServerName + "." + c.DNSDomain
	c.KeyName = c.ServerPrefix + "-login"
	c.FirewallName = c.ServerPrefix + "-ssh"
	c.SSHDir = filepath.Join(env.Home, ".ssh")
	c.StateDir = filepath.Join(env.Home, ".local", "state", "hetzner-claude")
	c.SSHConfig = filepath.Join(c.SSHDir, "config_hetzner_"+c.ServerName)
	c.KnownPin = filepath.Join(c.SSHDir, "known_hosts_hetzner_"+c.ServerName)
	c.IPState = filepath.Join(c.StateDir, "ip-"+c.ServerName)
	c.IDState = filepath.Join(c.StateDir, "id-"+c.ServerName)
	c.WorkspaceState = filepath.Join(c.StateDir, "workspace-"+c.ServerName)
	c.ReadyState = filepath.Join(c.StateDir, "ready-"+c.ServerName)
	c.RepoState = filepath.Join(c.StateDir, "repo-"+c.ServerName)
	c.EventsLog = filepath.Join(c.StateDir, "events.log")
	c.LogDir = filepath.Join(c.StateDir, "logs")
	return c, nil
}

// CheckDomain reports a DNS_DOMAIN that is not a plain lowercase domain.
func CheckDomain(d string) error {
	if !domainRe.MatchString(d) {
		return fmt.Errorf("DNS_DOMAIN must be a domain like example.com (lowercase, no scheme, no trailing dot): %s", d)
	}
	return nil
}

// KeyPath is the login key: SSH_KEY (default ~/.ssh/hetzner-build) with ~ expanded. A relative
// path is relative to the settings directory, where the scripts ran (they changed into it).
func KeyPath(setting, dir, home string) string {
	if setting == "" {
		return filepath.Join(home, ".ssh", "hetzner-build")
	}
	p := ExpandHome(setting, home)
	if !filepath.IsAbs(p) {
		if abs, err := filepath.Abs(filepath.Join(dir, p)); err == nil {
			p = abs
		}
	}
	return p
}

// ExpandHome turns a leading ~ into the home directory (${SSH_KEY/#\~/$HOME}).
func ExpandHome(p, home string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		return home + p[1:]
	}
	return p
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
