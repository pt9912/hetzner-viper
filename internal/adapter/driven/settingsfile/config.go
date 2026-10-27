package settingsfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

var (
	nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
)

// ProviderOf is the provider the settings files name: PROVIDER, in lower case, else the default.
// It is not read from the shell: the name is too common to take from the environment.
func ProviderOf(vars map[string]string) string {
	return or(strings.ToLower(strings.TrimSpace(vars["PROVIDER"])), model.DefaultProvider)
}

// tokens are the settings only the commands that talk to the cloud and Cloudflare need.
func tokens(p model.ProviderInfo) []string {
	return append(append([]string{}, p.Credentials...), "CLOUDFLARE_API_TOKEN", "CLOUDFLARE_ZONE_ID")
}

// LoadFile reads .env.local of dir (without a machine).
func LoadFile(dir string, env settings.Env) (map[string]string, error) {
	vars := map[string]string{}
	if err := sourceFile(vars, filepath.Join(dir, ".env.local"), env); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("No .env.local: run viper setup to create it")
		}
		return nil, err
	}
	return vars, nil
}

func sourceFile(vars map[string]string, path string, env settings.Env) error {
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

// Prefix is SERVER_PREFIX of a machine, read like the scripts read it (files only, the machine's
// file wins); a file that cannot be read counts as not set, so the default "claude" applies.
func Prefix(dir, machine string, env settings.Env) string {
	vars := map[string]string{}
	for _, f := range []string{filepath.Join(dir, ".env.local"), filepath.Join(dir, "machines", machine+".env.local")} {
		_ = sourceFile(vars, f, env)
	}
	return or(vars["SERVER_PREFIX"], "claude")
}

// Load reads .env.local and machines/<machine>.env.local of dir and checks the settings. With
// needTokens it requires the three API credentials as well (all commands that call an API).
func Load(dir, machine string, env settings.Env, needTokens bool) (*model.Machine, error) {
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
	c := &model.Machine{Name: machine, Dir: dir}
	c.Vars = func(name string) string {
		if v, ok := vars[name]; ok {
			return v
		}
		return env.Getenv(name)
	}

	c.Provider = ProviderOf(vars)
	prov, ok := model.LookupProvider(c.Provider)
	if !ok {
		return nil, fmt.Errorf("PROVIDER: unknown provider %s (one of: %s)", c.Provider, strings.Join(model.ProviderNames(), ", "))
	}
	if err := checkDiskSizes(c); err != nil {
		return nil, err
	}
	if err := checkRequired(c, prov, needTokens); err != nil {
		return nil, err
	}
	c.DNSDomain = c.Get("DNS_DOMAIN")
	if err := model.CheckDomain(c.DNSDomain); err != nil {
		return nil, err
	}
	c.SSHKey = model.KeyPath(c.Get("SSH_KEY"), dir, env.Home)
	c.ServerType = or(c.Get("SERVER_TYPE"), prov.Size)
	c.ServerLocation = or(c.Get("SERVER_LOCATION"), prov.Region)
	c.ServerImage = or(c.Get("SERVER_IMAGE"), prov.Image)
	c.WorkspaceDir = or(c.Get("WORKSPACE_DIR"), "/Development")
	c.ServerPrefix = or(c.Get("SERVER_PREFIX"), "claude")
	if !nameRe.MatchString(c.ServerPrefix) {
		return nil, fmt.Errorf("SERVER_PREFIX: lowercase letters, digits, hyphens: %s", c.ServerPrefix)
	}
	c.ServerName = c.ServerPrefix + "-" + machine
	if len(c.ServerName) > 63 {
		return nil, fmt.Errorf("Server name %s is longer than 63 characters", c.ServerName)
	}
	derivePaths(c, env.Home)
	return c, nil
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// Files reads the settings from the files in the settings directory; it is the adapter of the port
// internal/port/driven/settings.
type Files struct{}

// Load reads the settings of a machine; see Load.
func (Files) Load(dir, machine string, env settings.Env, needTokens bool) (*model.Machine, error) {
	return Load(dir, machine, env, needTokens)
}

// LoadFile reads .env.local of dir; see LoadFile.
func (Files) LoadFile(dir string, env settings.Env) (map[string]string, error) {
	return LoadFile(dir, env)
}

// Prefix is SERVER_PREFIX of a machine; see Prefix.
func (Files) Prefix(dir, machine string, env settings.Env) string { return Prefix(dir, machine, env) }

// ProviderOf is the provider the settings in vars name; see ProviderOf.
func (Files) ProviderOf(vars map[string]string) string { return ProviderOf(vars) }

// checkDiskSizes reports a disk size setting that is no whole number of GB (it would silently fall
// back to the default of the provider).
func checkDiskSizes(c *model.Machine) error {
	for _, name := range []string{"AWS_ROOT_GB", "GCP_ROOT_GB", "AZURE_ROOT_GB"} {
		if v := c.Get(name); v != "" {
			if n, err := strconv.Atoi(v); err != nil || n < 8 || n > 16384 {
				return fmt.Errorf("%s must be a whole number of GB between 8 and 16384: %s", name, v)
			}
		}
	}
	return nil
}

// checkRequired reports the settings that are missing, all at once.
func checkRequired(c *model.Machine, prov model.ProviderInfo, needTokens bool) error {
	required := []string{"DNS_DOMAIN"}
	if needTokens {
		required = append(required, tokens(prov)...)
	}
	var missing []string
	for _, v := range required {
		if c.Get(v) == "" {
			missing = append(missing, v)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("Missing settings: %s (in .env.local or machines/%s.env.local; the three tokens may also be exported in the shell)", strings.Join(missing, " "), c.Name)
	}
	return nil
}

// derivePaths sets the names and paths that follow from the server name.
func derivePaths(c *model.Machine, home string) {
	// Everything per machine is keyed by the server name (prefix + machine), so equal machine
	// names under different prefixes never share a DNS record or local files.
	c.SSHAlias = "hetzner-" + c.ServerName
	c.DNSHost = c.ServerName + "." + c.DNSDomain
	c.KeyName = c.ServerPrefix + "-login"
	c.FirewallName = c.ServerPrefix + "-ssh"
	c.SSHDir = filepath.Join(home, ".ssh")
	c.StateDir = filepath.Join(home, ".local", "state", "hetzner-claude")
	c.SSHConfig = filepath.Join(c.SSHDir, "config_hetzner_"+c.ServerName)
	c.KnownPin = filepath.Join(c.SSHDir, "known_hosts_hetzner_"+c.ServerName)
	c.IPState = filepath.Join(c.StateDir, "ip-"+c.ServerName)
	c.IDState = filepath.Join(c.StateDir, "id-"+c.ServerName)
	c.WorkspaceState = filepath.Join(c.StateDir, "workspace-"+c.ServerName)
	c.ReadyState = filepath.Join(c.StateDir, "ready-"+c.ServerName)
	c.RepoState = filepath.Join(c.StateDir, "repo-"+c.ServerName)
	c.EventsLog = filepath.Join(c.StateDir, "events.log")
	c.LogDir = filepath.Join(c.StateDir, "logs")
}
