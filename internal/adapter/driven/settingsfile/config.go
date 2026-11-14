package settingsfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

var (
	nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
)

// DNSProviderOf is the DNS provider the settings files name: DNS_PROVIDER, in lower case, else
// Cloudflare. Like PROVIDER it is not read from the shell.
func DNSProviderOf(vars map[string]string) string {
	return or(strings.ToLower(strings.TrimSpace(vars["DNS_PROVIDER"])), model.DNSCloudflare)
}

// ProviderOf is the provider the settings files name: PROVIDER, in lower case, else the default.
// It is not read from the shell: the name is too common to take from the environment.
func ProviderOf(vars map[string]string) string {
	return or(strings.ToLower(strings.TrimSpace(vars["PROVIDER"])), model.DefaultProvider)
}

// tokens are the settings only the commands that talk to the cloud (and Cloudflare, if viper makes
// the DNS name there) need.
func tokens(p model.ProviderInfo, dnsProvider string) []string {
	out := append([]string{}, p.Credentials...)
	if dnsProvider == model.DNSCloudflare {
		out = append(out, "CLOUDFLARE_API_TOKEN", "CLOUDFLARE_ZONE_ID")
	}
	return out
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
// file wins); a file that cannot be read counts as not set, so the default "claude" applies. The
// choice between groups is not swallowed: a machine that several groups name, with no group chosen
// (or a wrong one), has no prefix to tell, and an empty log would be the wrong answer.
func Prefix(dir, machine string, env settings.Env) (string, error) {
	vars := map[string]string{}
	_ = sourceFile(vars, filepath.Join(dir, ".env.local"), env)
	grp, err := memberOf(dir, machine, env)
	var ce choiceError
	if errors.As(err, &ce) {
		if g := startedIn(dir, machine, env, ce.groups); env.Group == "" && g != "" { // the group it runs in applies
			env.Group = g
			return Prefix(dir, machine, env)
		}
		return "", ce.error
	}
	if err == nil && grp.file != "" {
		_ = sourceFile(vars, grp.file, env)
	}
	_ = sourceFile(vars, filepath.Join(dir, "machines", machine+".env.local"), env)
	return or(vars["SERVER_PREFIX"], "claude"), nil
}

// Load reads .env.local and machines/<machine>.env.local of dir and checks the settings. With
// needTokens it requires the three API credentials as well (all commands that call an API).
func Load(dir, machine string, env settings.Env, needTokens bool) (*model.Machine, error) {
	c, err := load(dir, machine, env, needTokens)
	var ce choiceError
	if err != nil && env.Group == "" && errors.As(err, &ce) && len(ce.groups) > 1 {
		// several groups name the machine and none was said: the one it was started in applies
		if g := startedIn(dir, machine, env, ce.groups); g != "" {
			env.Group = g
			return Load(dir, machine, env, needTokens)
		}
	}
	if err == nil {
		err = runsElsewhere(dir, c, env)
	}
	if err != nil {
		return nil, err
	}
	moveOldState(c)
	return c, nil
}

// moveOldState moves the state files that older versions kept in the state directory itself (v0.21.0 and before) or in
// a directory of the prefix there (v0.21.1) into servers/<prefix> (best effort: a file that cannot be moved is read from
// its new place and found missing, as if it was never there).
func moveOldState(c *model.Machine) {
	_ = os.MkdirAll(c.MachineStateDir, 0o700)
	for _, f := range []string{c.IPState, c.IDState, c.WorkspaceState, c.ReadyState, c.RepoState, c.GroupState} {
		if _, err := os.Stat(f); err == nil {
			continue
		}
		for _, old := range []string{filepath.Join(c.StateDir, c.ServerPrefix, filepath.Base(f)), filepath.Join(c.StateDir, filepath.Base(f))} {
			if _, err := os.Stat(old); err == nil {
				_ = os.Rename(old, f)
				break
			}
		}
	}
}

// startedIn is the group, of those that name the machine, that it was started in and still runs in ("" if none or
// not one alone).
func startedIn(dir, machine string, env settings.Env, groups []string) string {
	found := ""
	for _, g := range groups {
		e := env
		e.Group = g
		if c, err := load(dir, machine, e, false); err == nil && readTrim(c.GroupState) == g {
			if found != "" {
				return ""
			}
			found = g
		}
	}
	return found
}

// runsElsewhere refuses a machine that was started in another group: it is one server, managed in the group it was
// started in, until viper down. A group that no longer names it does not hold it.
func runsElsewhere(dir string, c *model.Machine, env settings.Env) error {
	rec := readTrim(c.GroupState)
	if rec == "" || c.Group == "" || rec == c.Group {
		return nil
	}
	if g, err := LoadGroup(dir, rec, env); err != nil || !slices.Contains(g.Machines, c.Name) {
		return nil
	}
	return &model.ElsewhereError{Machine: c.Name, Running: rec, Asked: c.Group}
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func load(dir, machine string, env settings.Env, needTokens bool) (*model.Machine, error) {
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
	grp, err := memberOf(dir, machine, env)
	if err != nil {
		return nil, err
	}
	if err := applyGroup(vars, grp, env); err != nil {
		return nil, err
	}
	if err := sourceFile(vars, filepath.Join(dir, "machines", machine+".env.local"), env); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := checkMachineGroup(vars, grp, machine); err != nil {
		return nil, err
	}
	applyDefaultImage(vars, env)
	c := &model.Machine{Name: machine, Dir: dir, Group: grp.name, GroupChosen: grp.several, GroupFile: grp.file, Warnings: grp.warnings}
	c.Vars = func(name string) string {
		if v, ok := vars[name]; ok {
			return v
		}
		return env.Getenv(name)
	}

	prov, err := applyProviders(c, vars, needTokens)
	if err != nil {
		return nil, err
	}
	c.SSHKey = model.KeyPath(c.Get("SSH_KEY"), dir, env.Home)
	c.ServerType = or(c.Get("SERVER_TYPE"), prov.Size)
	c.ServerLocation = or(c.Get("SERVER_LOCATION"), prov.Region)
	c.ServerImage, c.WorkspaceDir = imageAndWorkspace(c, prov)
	c.ServerPrefix = or(c.Get("SERVER_PREFIX"), "claude")
	if !nameRe.MatchString(c.ServerPrefix) {
		return nil, fmt.Errorf("SERVER_PREFIX: lowercase letters, digits, hyphens: %s", c.ServerPrefix)
	}
	c.ServerName = c.ServerPrefix + "-" + machine
	if len(c.ServerName) > 63 {
		return nil, fmt.Errorf("Server name %s is longer than 63 characters", c.ServerName)
	}
	if err := applyNATS(c, env.Home); err != nil {
		return nil, err
	}
	derivePaths(c, env.Home)
	return c, nil
}

// applyNATS reads NATS_ENABLED, NATS_GROUP and NATS_PORT. With NATS off nothing else is checked, so
// the settings of a machine without NATS are read as before.
func applyNATS(c *model.Machine, home string) error {
	on, err := model.ParseNATSBool("NATS_ENABLED", c.Get("NATS_ENABLED"))
	if err != nil || !on {
		return err
	}
	if info, _ := model.LookupProvider(c.Provider); !info.NATS {
		return fmt.Errorf("NATS_ENABLED: NATS groups need PROVIDER=%s for now, not %s", strings.Join(model.NATSProviderNames(), " or "), c.Provider)
	}
	group := strings.TrimSpace(c.Get("NATS_GROUP"))
	if group == "" {
		return errors.New("NATS_GROUP: required when NATS_ENABLED is on")
	}
	if err := model.CheckNATSName("NATS_GROUP", group); err != nil {
		return err
	}
	if err := model.CheckNATSName("Machine name (NATS_ENABLED is on)", c.Name); err != nil {
		return err
	}
	port, err := model.ParseNATSPort(c.Get("NATS_PORT"))
	if err != nil {
		return err
	}
	operator := model.DefaultNATSOperator
	if v := strings.TrimSpace(c.Get("NATS_OPERATOR_NAME")); v != "" {
		operator = v
	}
	if err := model.CheckNATSName("NATS_OPERATOR_NAME", operator); err != nil {
		return err
	}
	if operator == c.Name {
		return fmt.Errorf("Machine name %q is the name of the operator of the group (NATS_OPERATOR_NAME): use another machine name or another operator name", c.Name)
	}
	c.NATS = model.NATSConfig{Enabled: true, Group: group, Port: port, Operator: operator, MCPAutoAllow: model.AutoAllowOff}
	if err := applyNATSObjects(c); err != nil {
		return err
	}
	if err := applyNATSMCP(c, home); err != nil {
		return err
	}
	if c.NATS.Objects && !c.NATS.MCP {
		return errors.New("NATS_OBJECTS: needs NATS_MCP=true (the programs that put and get the files are part of viper-nats-mcp)")
	}
	return nil
}

// applyNATSObjects reads NATS_OBJECTS and its limits. They are looked at only if NATS_OBJECTS is on.
func applyNATSObjects(c *model.Machine) error {
	on, err := model.ParseNATSBool("NATS_OBJECTS", c.Get("NATS_OBJECTS"))
	if err != nil || !on {
		return err
	}
	c.NATS.Objects = true
	c.NATS.ObjectsMax, c.NATS.ObjectsFileMax, c.NATS.ObjectsTTLDays, err = model.ParseObjectsLimits(c.Get("NATS_OBJECTS_MAX"), c.Get("NATS_OBJECTS_FILE_MAX"), c.Get("NATS_OBJECTS_TTL"))
	return err
}

// mcpTopics reads NATS_MCP_TOPICS: names separated by commas, without duplicates, at most model.MaxMCPTopics.
func mcpTopics(list string) ([]string, error) {
	var topics []string
	seen := map[string]bool{}
	for _, t := range strings.Split(list, ",") {
		if t = strings.TrimSpace(t); t == "" || seen[t] {
			continue
		}
		if err := model.CheckNATSName("NATS_MCP_TOPICS", t); err != nil {
			return nil, err
		}
		seen[t] = true
		topics = append(topics, t)
	}
	if len(topics) > model.MaxMCPTopics {
		return nil, fmt.Errorf("NATS_MCP_TOPICS: at most %d topics", model.MaxMCPTopics)
	}
	return topics, nil
}

// applyNATSMCP reads NATS_MCP and its settings. They are looked at only if NATS_MCP is on, like the
// settings of every other feature that is off.
func applyNATSMCP(c *model.Machine, home string) error {
	on, err := model.ParseNATSBool("NATS_MCP", c.Get("NATS_MCP"))
	if err != nil || !on {
		return err
	}
	c.NATS.MCP = true
	seen := map[string]bool{}
	for _, m := range strings.Split(c.Get("NATS_MCP_PUSH_FROM"), ",") {
		if m = strings.TrimSpace(m); m == "" || seen[m] {
			continue
		}
		if err := model.CheckNATSName("NATS_MCP_PUSH_FROM", m); err != nil {
			return err
		}
		if m == c.Name {
			continue // the same list serves all machines of a group: the machine itself is not a source for itself
		}
		seen[m] = true
		c.NATS.MCPPushFrom = append(c.NATS.MCPPushFrom, m)
	}
	if c.NATS.MCPTopics, err = mcpTopics(c.Get("NATS_MCP_TOPICS")); err != nil {
		return err
	}
	mode := strings.ToLower(strings.TrimSpace(c.Get("NATS_MCP_AUTOALLOW")))
	switch mode {
	case "":
	case model.AutoAllowOff, model.AutoAllowRead, model.AutoAllowAll:
		c.NATS.MCPAutoAllow = mode
	default:
		return fmt.Errorf("NATS_MCP_AUTOALLOW: off, read or all: %s", mode)
	}
	if f := strings.TrimSpace(c.Get("NATS_MCP_BINARY")); f != "" {
		c.NATS.MCPBinary = model.ExpandHome(f, home)
	}
	return nil
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
func (Files) Prefix(dir, machine string, env settings.Env) (string, error) {
	return Prefix(dir, machine, env)
}

// LoadGroup reads groups/<name>.env; see LoadGroup.
func (Files) LoadGroup(dir, name string, env settings.Env) (*model.Group, error) {
	return LoadGroup(dir, name, env)
}

// ProviderOf is the provider the settings in vars name; see ProviderOf.
func (Files) ProviderOf(vars map[string]string) string { return ProviderOf(vars) }

// DNSProviderOf is the DNS provider the settings in vars name; see DNSProviderOf.
func (Files) DNSProviderOf(vars map[string]string) string { return DNSProviderOf(vars) }

// diskSize is the setting for the size of the system disk of a provider that has one, and the
// smallest size the provider's Ubuntu image fits on; ok is false for a provider without one.
func diskSize(provider string) (name string, smallest int, ok bool) {
	switch provider {
	case "aws":
		return "AWS_ROOT_GB", 8, true
	case "gcp":
		return "GCP_ROOT_GB", 10, true
	case "azure":
		return "AZURE_ROOT_GB", 30, true
	case "kubernetes":
		return "K8S_DISK_GB", 1, true
	}
	return "", 0, false
}

// checkDiskSizes reports the disk size setting of the machine's provider if it is no whole number
// of GB (it would silently fall back to the default of the provider) or too small for the image.
func checkDiskSizes(c *model.Machine) error {
	name, lowest, ok := diskSize(c.Provider)
	if !ok {
		return nil
	}
	if v := c.Get(name); v != "" {
		if n, err := strconv.Atoi(v); err != nil || n < lowest || n > 16384 {
			return fmt.Errorf("%s must be a whole number of GB between %d and 16384: %s", name, lowest, v)
		}
	}
	return nil
}

// checkRequired reports the settings that are missing, all at once.
func checkRequired(c *model.Machine, prov model.ProviderInfo, needTokens bool) error {
	var required []string
	if c.DNSProvider == model.DNSCloudflare {
		required = append(required, "DNS_DOMAIN")
	}
	if needTokens {
		required = append(required, tokens(prov, c.DNSProvider)...)
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
	c.StateDir = model.StateDir(home)
	c.MachineStateDir = filepath.Join(c.StateDir, "servers", c.ServerPrefix) // the files of the machines of one prefix lie together
	c.SSHConfig = filepath.Join(c.SSHDir, "config_hetzner_"+c.ServerName)
	c.KnownPin = filepath.Join(c.SSHDir, "known_hosts_hetzner_"+c.ServerName)
	c.IPState = filepath.Join(c.MachineStateDir, "ip-"+c.ServerName)
	c.IDState = filepath.Join(c.MachineStateDir, "id-"+c.ServerName)
	c.WorkspaceState = filepath.Join(c.MachineStateDir, "workspace-"+c.ServerName)
	c.ReadyState = filepath.Join(c.MachineStateDir, "ready-"+c.ServerName)
	c.RepoState = filepath.Join(c.MachineStateDir, "repo-"+c.ServerName)
	c.GroupState = filepath.Join(c.MachineStateDir, "group-"+c.ServerName)
	c.EventsLog = filepath.Join(c.StateDir, "events.log")
	c.LogDir = filepath.Join(c.StateDir, "logs")
}

// applyDefaultImage gives a machine in a cluster or on a Docker host the image that was published for this version of viper
// when no file and no environment variable names one. A build that is not a release has none: the image is then a required
// setting (viper image build makes it).
func applyDefaultImage(vars map[string]string, env settings.Env) {
	name := map[string]string{"kubernetes": "K8S_IMAGE", "compose": "COMPOSE_IMAGE"}[ProviderOf(vars)]
	if name == "" || vars[name] != "" || env.Getenv(name) != "" {
		return
	}
	if img := model.DefaultMachineImage(env.Version); img != "" {
		vars[name] = img
	}
}

// imageAndWorkspace are SERVER_IMAGE and WORKSPACE_DIR of the machine. The work directory is below the home directory of
// builder unless WORKSPACE_DIR says otherwise (a machine that is a container has no root to make one elsewhere). A machine
// that is a container has the image of the container (K8S_IMAGE or COMPOSE_IMAGE).
func imageAndWorkspace(c *model.Machine, prov model.ProviderInfo) (image, workspace string) {
	image, workspace = or(c.Get("SERVER_IMAGE"), prov.Image), or(c.Get("WORKSPACE_DIR"), DefaultWorkspace)
	switch c.Provider {
	case "kubernetes":
		image = c.Get("K8S_IMAGE")
	case "compose":
		image = c.Get("COMPOSE_IMAGE")
	}
	return image, workspace
}

// secretNameRe is the name of a Kubernetes Secret.
var secretNameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)

var (
	namespaceRe  = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	imageRefRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]*$`)
	dockerHostRe = regexp.MustCompile(`^(tcp://[A-Za-z0-9._-]+:[0-9]{1,5}|unix:///[A-Za-z0-9._/-]+|ssh://[A-Za-z0-9._@-]+(:[0-9]{1,5})?)$`)
)

// DefaultWorkspace is WORKSPACE_DIR when it is not set: below the home directory of builder, which is the volume of a
// container and needs no root on a VM. A container accepts no other place.
const DefaultWorkspace = "/home/builder/work"

// checkKubernetesNames checks the image and the context, which go into docker, k3d and kubeconfig calls: neither may be
// taken for an option.
func checkKubernetesNames(c *model.Machine) error {
	if v := c.Get("K8S_IMAGE"); !imageRefRe.MatchString(v) {
		return fmt.Errorf("K8S_IMAGE: an image reference without blanks or quotes, not beginning with a dash: %s", v)
	}
	loc := c.Get("SERVER_LOCATION")
	if strings.HasPrefix(loc, "-") || (strings.HasPrefix(loc, "k3d-") && !k3dClusterNameRe.MatchString(strings.TrimPrefix(loc, "k3d-"))) {
		return fmt.Errorf("SERVER_LOCATION: the context of the kubeconfig (for k3d k3d-<cluster> with a cluster name of letters, digits, - . _): %s", loc)
	}
	return nil
}

// k3dClusterNameRe is the name of a k3d cluster in the context k3d-<cluster>.
var k3dClusterNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// checkK3dValues checks the settings of a k3d cluster: the registry viper makes for the image, whether viper makes the
// cluster, and how many agent nodes it gets. They are settings of the machine (or of its group), like all others; a
// machine that is not in a k3d cluster ignores them.
func checkK3dValues(c *model.Machine) error {
	if v := c.Get("K3D_REGISTRY"); v != "" && !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{0,62}$`).MatchString(v) {
		return fmt.Errorf("K3D_REGISTRY: the name of a k3d registry (letters, digits and -): %s", v)
	}
	if v := c.Get("K3D_CLUSTER_CREATE"); v != "" && v != "0" && v != "1" {
		return fmt.Errorf("K3D_CLUSTER_CREATE: 0 or 1: %s", v)
	}
	if v := c.Get("K3D_AGENTS"); v != "" {
		if n, err := strconv.Atoi(v); err != nil || n < 0 || n > 16 {
			return fmt.Errorf("K3D_AGENTS must be a whole number of agent nodes between 0 and 16: %s", v)
		}
	}
	return nil
}

// checkKubernetesValues checks the settings of a pod that are a name or a number.
func checkKubernetesValues(c *model.Machine) error {
	for _, v := range []struct {
		name string
		re   *regexp.Regexp
		what string
	}{
		{"K8S_NAMESPACE", namespaceRe, "the name of a namespace (lowercase letters, digits and -, at most 63)"},
		{"K8S_STORAGE_CLASS", secretNameRe, "the name of a storage class (lowercase letters, digits, - and .)"},
		{"K8S_DOCKER_IMAGE", imageRefRe, "an image reference without blanks or quotes"},
		{"K8S_DOCKER_HOST", dockerHostRe, "tcp://host:port, unix:///path or ssh://host"},
	} {
		if val := c.Get(v.name); val != "" && !v.re.MatchString(val) {
			return fmt.Errorf("%s: %s: %s", v.name, v.what, val)
		}
	}
	if v := c.Get("K8S_IMAGE_PULL_SECRET"); v != "" && !secretNameRe.MatchString(v) {
		return fmt.Errorf("K8S_IMAGE_PULL_SECRET: the name of a Secret (lowercase letters, digits, - and .): %s", v)
	}
	if err := checkK3dValues(c); err != nil {
		return err
	}
	if v := c.Get("K8S_BREW_GB"); v != "" {
		if n, err := strconv.Atoi(v); err != nil || n < 1 || n > 16384 {
			return fmt.Errorf("K8S_BREW_GB must be a whole number of GB between 1 and 16384: %s", v)
		}
	}
	return nil
}

// checkKubernetes reports what a machine in a Kubernetes cluster needs (docs/plan/kubernetes.md): the cluster, the
// image, no DNS (a pod has no public address) and a work directory that is on its volume.
func checkKubernetes(c *model.Machine) error {
	var missing []string
	for _, name := range []string{"SERVER_LOCATION", "K8S_IMAGE"} {
		if c.Get(name) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("Missing settings: %s (PROVIDER=kubernetes: SERVER_LOCATION is the context of the kubeconfig, K8S_IMAGE the image of the machine, which viper image build makes; a released viper takes ghcr.io/pt9912/viper-machine:<its version> itself, any other build needs it; viper takes no default context so that nothing is made in the wrong cluster)", strings.Join(missing, " "))
	}
	if DNSProviderOf(map[string]string{"DNS_PROVIDER": c.Get("DNS_PROVIDER")}) != model.DNSNone {
		return errors.New("DNS_PROVIDER=none is needed for PROVIDER=kubernetes: a pod has no public address to name")
	}
	if err := checkKubernetesNames(c); err != nil {
		return err
	}
	if err := checkKubernetesValues(c); err != nil {
		return err
	}
	if v := c.Get("VIPER_SWAP"); v != "" && v != "0" {
		return errors.New("VIPER_SWAP: a pod has no swap file to make (PROVIDER=kubernetes)")
	}
	if c.Get("VIPER_CODE_SERVER") == "1" {
		return errors.New("VIPER_CODE_SERVER: code-server needs root and systemd, which a pod does not have (PROVIDER=kubernetes); VS Code Remote-SSH works without it")
	}
	switch c.Get("K8S_DOCKER") {
	case "", "dind", "remote", "none":
	default:
		return fmt.Errorf("K8S_DOCKER: dind, remote or none: %s", c.Get("K8S_DOCKER"))
	}
	if ws := c.Setting("WORKSPACE_DIR", DefaultWorkspace); !strings.HasPrefix(ws, "/home/builder/") || strings.Contains(ws, "..") {
		return fmt.Errorf("WORKSPACE_DIR: for PROVIDER=kubernetes a directory below /home/builder, which is the volume of the pod (default %s); a WORKSPACE_DIR in .env.local is for the VMs: set it for this machine in machines/%s.env.local or in its group file: %s", DefaultWorkspace, c.Name, ws)
	}
	return nil
}

// checkComposeValues checks the settings of a container on a Docker host that are a name or an address.
func checkComposeValues(c *model.Machine) error {
	for _, v := range []struct {
		name string
		re   *regexp.Regexp
		what string
	}{
		{"COMPOSE_IMAGE", imageRefRe, "an image reference without blanks or quotes"},
		{"COMPOSE_DOCKER_IMAGE", imageRefRe, "an image reference without blanks or quotes"},
		{"COMPOSE_DOCKER_HOST", dockerHostRe, "tcp://host:port, unix:///path or ssh://host"},
	} {
		if val := c.Get(v.name); val != "" && !v.re.MatchString(val) {
			return fmt.Errorf("%s: %s: %s", v.name, v.what, val)
		}
	}
	return nil
}

// checkComposeDocker checks COMPOSE_DOCKER and what goes with it.
func checkComposeDocker(c *model.Machine) error {
	switch c.Get("COMPOSE_DOCKER") {
	case "", "dind", "none":
	case "remote":
		if c.Get("COMPOSE_DOCKER_HOST") == "" {
			return errors.New("COMPOSE_DOCKER=remote needs COMPOSE_DOCKER_HOST (where the Docker daemon is, like tcp://host:2375)")
		}
	default:
		return fmt.Errorf("COMPOSE_DOCKER: dind, remote or none: %s", c.Get("COMPOSE_DOCKER"))
	}
	return nil
}

// checkCompose reports what a machine on a Docker host needs (docs/plan/compose.md): the Docker context, the image, no DNS
// (a container has no public address) and a work directory that is on its volume.
func checkCompose(c *model.Machine) error {
	var missing []string
	for _, name := range []string{"SERVER_LOCATION", "COMPOSE_IMAGE"} {
		if c.Get(name) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("Missing settings: %s (PROVIDER=compose: SERVER_LOCATION is the Docker context, \"default\" for the local daemon, COMPOSE_IMAGE the image of the machine, which viper image build makes; a released viper takes ghcr.io/pt9912/viper-machine:<its version> itself, any other build needs it; viper takes no default Docker context so that nothing is made on the wrong host)", strings.Join(missing, " "))
	}
	if strings.HasPrefix(c.Get("SERVER_LOCATION"), "-") || strings.ContainsAny(c.Get("SERVER_LOCATION"), " \t\n\r\"'") {
		return fmt.Errorf("SERVER_LOCATION: the name of a Docker context: %s", c.Get("SERVER_LOCATION"))
	}
	if DNSProviderOf(map[string]string{"DNS_PROVIDER": c.Get("DNS_PROVIDER")}) != model.DNSNone {
		return errors.New("DNS_PROVIDER=none is needed for PROVIDER=compose: a container has no public address to name")
	}
	if err := checkComposeValues(c); err != nil {
		return err
	}
	if v := c.Get("VIPER_SWAP"); v != "" && v != "0" {
		return errors.New("VIPER_SWAP: a container has no swap file to make (PROVIDER=compose)")
	}
	if c.Get("VIPER_CODE_SERVER") == "1" {
		return errors.New("VIPER_CODE_SERVER: code-server needs root and systemd, which a container does not have (PROVIDER=compose); VS Code Remote-SSH works without it")
	}
	if err := checkComposeDocker(c); err != nil {
		return err
	}
	if ws := c.Setting("WORKSPACE_DIR", DefaultWorkspace); !strings.HasPrefix(ws, "/home/builder/") || strings.Contains(ws, "..") {
		return fmt.Errorf("WORKSPACE_DIR: for PROVIDER=compose a directory below /home/builder, which is the volume of the container (default %s); set it for this machine in machines/%s.env.local or in its group file: %s", DefaultWorkspace, c.Name, ws)
	}
	return nil
}

// applyProviders sets the cloud provider and the DNS provider of the machine and the domain that
// follows from them, and checks what they need. It returns what is known of the cloud provider.
func applyProviders(c *model.Machine, vars map[string]string, needTokens bool) (model.ProviderInfo, error) {
	c.Provider = ProviderOf(vars)
	prov, ok := model.LookupProvider(c.Provider)
	if !ok {
		return model.ProviderInfo{}, fmt.Errorf("PROVIDER: unknown provider %s (one of: %s)", c.Provider, strings.Join(model.ProviderNames(), ", "))
	}
	if err := checkDiskSizes(c); err != nil {
		return prov, err
	}
	if c.Provider == "aws" {
		for _, name := range []string{"AWS_TAGS", "AWS_TAGS_SHARED"} {
			if _, err := model.ParseAWSTags(name, c.Get(name)); err != nil {
				return prov, err
			}
		}
	}
	c.DNSProvider = DNSProviderOf(vars)
	switch c.Provider {
	case "kubernetes":
		if err := checkKubernetes(c); err != nil {
			return prov, err
		}
	case "compose":
		if err := checkCompose(c); err != nil {
			return prov, err
		}
	}
	if c.DNSProvider != model.DNSCloudflare && c.DNSProvider != model.DNSNone {
		return prov, fmt.Errorf("DNS_PROVIDER: %s or %s: %s", model.DNSCloudflare, model.DNSNone, c.DNSProvider)
	}
	if err := checkRequired(c, prov, needTokens); err != nil {
		return prov, err
	}
	c.DNSDomain = c.Get("DNS_DOMAIN")
	if c.DNSProvider == model.DNSNone {
		c.DNSDomain = model.NoDNSDomain
	} else if err := model.CheckDomain(c.DNSDomain); err != nil {
		return prov, err
	}
	return prov, nil
}
