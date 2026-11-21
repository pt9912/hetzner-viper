package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/natsserver"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

// viper nats tunnel (docs/plan/nats-lokal.md, §4.1): the helper of viper-nats-mcp --via-viper. It opens the ssh tunnel to the NATS server of a
// group for an identity of this computer (viper nats init --name), says on one line of stdout where to connect, and stays until its
// stdin ends or it is stopped. It is not meant for people.

// DefaultLocalName is the name of the identity of a Claude session on this computer.
const DefaultLocalName = "local"

// exitNeedsYes is the exit status of viper nats tunnel when a machine would get the text of this identity pushed into its session and
// --yes was not given: the program that started it can tell it apart from a failure.
const exitNeedsYes = 3

// maxReadyLine is the longest line viper nats tunnel writes; the reader refuses more.
const maxReadyLine = 4096

// tunnelReady is the one line viper nats tunnel writes on stdout once the tunnel is open.
type tunnelReady struct {
	Event          string `json:"event"`
	Version        string `json:"version"`
	Group          string `json:"group"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	ServerName     string `json:"server_name,omitempty"`
	CAFile         string `json:"ca_file,omitempty"`
	SeedFile       string `json:"seed_file"`
	SettingsDir    string `json:"settings_dir"`
	StateDir       string `json:"state_dir"`
	ObjectsFileMax int64  `json:"objects_file_max"`
	Server         string `json:"server,omitempty"`
	PID            int    `json:"pid"`
}

// walkUpLevels is how many folders above the current one are looked at for the settings of a group.
const walkUpLevels = 5

// findGroupDir says which folder holds the settings of the group (groups/<group>.env): --dir, the folder that viper nats init
// remembered, or a folder above the current one (only for an identity that has a key here). It names the way it was found.
func findGroupDir(o *options, env settings.Env, group, name string, cwdFirst bool) (dir, how string, err error) {
	has := func(d string) bool { return exists(filepath.Join(d, "groups", group+".env")) }
	state := model.StateDir(env.Home)
	var checked []string
	if o.dir != "" {
		return explicitDir(o.dir, group)
	}
	checked = append(checked, "--dir (not given)")
	if cwdFirst { // the commands of a person: the current folder is the folder with the settings, as for every other command
		if cwd, err := os.Getwd(); err == nil && has(cwd) {
			return cwd, "the current folder", nil
		}
		checked = append(checked, "the current folder (no groups/"+group+".env)")
	}
	if raw, rerr := os.ReadFile(model.SettingsDirFile(state, group)); rerr == nil {
		d := strings.TrimSpace(string(raw))
		if filepath.IsAbs(d) && has(d) {
			return d, "the folder viper nats init remembered", nil
		}
		return "", "", fmt.Errorf("viper nats init remembered %s for the group %s, but it has no groups/%s.env there any more (moved?): run viper nats init %s --name %s in the folder with the settings", d, group, group, group, name)
	}
	checked = append(checked, "the folder viper nats init remembers (none)")
	if !model.HasIdentityKey(state, group, model.DefaultNATSOperator, name) {
		return "", "", notFoundError(group, name, checked, "folders above the current one (not looked at: no key for "+name+" here)")
	}
	return walkUp(env, group, name, checked, has)
}

// explicitDir checks the folder --dir names.
func explicitDir(dir, group string) (string, string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", "", err
	}
	if !exists(filepath.Join(abs, "groups", group+".env")) {
		return "", "", fmt.Errorf("--dir %s has no groups/%s.env", abs, group)
	}
	return abs, "--dir", nil
}

// walkUp looks in the folders above the current one, at most walkUpLevels of them and never above the home folder.
func walkUp(env settings.Env, group, name string, checked []string, has func(string) bool) (string, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	if !within(env.Home, cwd) { // outside the home folder nothing is looked at: a folder there is not the user's
		return "", "", notFoundError(group, name, checked, fmt.Sprintf("%s is not below %s: folders above it are not looked at", cwd, env.Home))
	}
	d := cwd
	for i := 0; i <= walkUpLevels; i++ {
		if has(d) {
			return d, "the folder above the current one", nil
		}
		parent := filepath.Dir(d)
		if d == env.Home || parent == d || !within(env.Home, parent) {
			break
		}
		d = parent
	}
	return "", "", notFoundError(group, name, checked, fmt.Sprintf("%s and up to %d folders above it, not above %s", cwd, walkUpLevels, env.Home))
}

// within tells whether path is dir or below it.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func notFoundError(group, name string, checked []string, last string) error {
	return fmt.Errorf("the group %s was not found (looked at: %s, %s). Run viper in the folder with the settings, give --dir ABSOLUTE-FOLDER, or run viper nats init %s --name %s in the folder with the settings",
		group, strings.Join(checked, ", "), last, group, name)
}

// tunnelIdentity checks that this computer has the key of the identity and that the server has it registered with that key.
func tunnelIdentity(g *natsGroup, name string, registered map[string]string, onlyNames []string) error {
	seed, err := os.ReadFile(g.keyOf(name))
	if err != nil {
		return fmt.Errorf("%s has no key on this computer (%s): run viper nats init %s --name %s", name, g.keyOf(name), g.name, name)
	}
	pub, err := natsserver.PublicKeyOf(string(seed))
	if err != nil {
		return fmt.Errorf("%s: %w", g.keyOf(name), err)
	}
	key, ok := registered[name]
	switch {
	case !ok && !slices.Contains(onlyNames, name):
		return fmt.Errorf("%s is not registered at the server of the group %s: run viper nats init %s --name %s", name, g.name, g.name, name)
	case ok && key != pub:
		return fmt.Errorf("the server has another key for %s than this computer (another computer ran viper nats init, or the group was made again): run viper nats init %s --name %s", name, g.name, name)
	}
	return nil
}

// pushTargets are the machines that put the text of name into the session of their agent (NATS_MCP_PUSH_FROM).
func (g *natsGroup) pushTargets(name string) []string {
	var hit []string
	for _, m := range g.order {
		if c, ok := g.ms[m]; ok && slices.Contains(c.cfg.NATS.MCPPushFrom, name) {
			hit = append(hit, m)
		}
	}
	return hit
}

// runNatsTunnelCmd is viper nats tunnel: the tunnel itself, or with --print-config the entry for Claude Code.
func runNatsTunnelCmd(ctx context.Context, o *options, env settings.Env, group string, in io.Reader, stdout, stderr io.Writer) error {
	if o.printConfig {
		return runNatsPrintConfig(o, env, group, stdout)
	}
	if o.pushFrom != "" || o.files != "" {
		return errors.New("viper nats tunnel: --push-from and --files belong to --print-config (they go into the entry for Claude Code)")
	}
	return runNatsTunnel(ctx, o, env, group, in, stdout, stderr)
}

// tunnelTarget finds the group and checks the identity: the group with the identity chosen, the settings folder and the server.
func tunnelTarget(ctx context.Context, o *options, env settings.Env, group, name string, stderr io.Writer) (*natsGroup, string, error) {
	if err := model.CheckNATSName("group", group); err != nil {
		return nil, "", err
	}
	if err := model.CheckNATSName("identity", name); err != nil {
		return nil, "", err
	}
	dir, how, err := findGroupDir(o, env, group, name, false)
	if err != nil {
		return nil, "", err
	}
	o.dir = dir
	fmt.Fprintf(stderr, "The group %s: the settings are in %s (%s).\n", group, dir, how)
	g, err := loadNatsGroup(o, env, group)
	if err != nil {
		return nil, "", err
	}
	g.useIdentity(name)
	srv, err := g.find(ctx, o)
	if err != nil {
		return nil, "", err
	}
	if err := tunnelIdentity(g, name, srv.OperatorKeys, srv.Operators); err != nil {
		return nil, "", err
	}
	return g, dir, nil
}

// runNatsTunnel is viper nats tunnel GROUP [--name NAME] [--port N] [--yes].
func runNatsTunnel(ctx context.Context, o *options, env settings.Env, group string, stdin io.Reader, stdout, stderr io.Writer) error {
	name := o.name
	if name == "" {
		name = DefaultLocalName
	}
	ctx, stop := notifyContext(ctx)
	defer stop()
	g, dir, err := tunnelTarget(ctx, o, env, group, name, stderr)
	if err != nil {
		return err
	}
	if hit := g.pushTargets(name); len(hit) > 0 && !o.yes {
		fmt.Fprintf(stderr, "%s is in NATS_MCP_PUSH_FROM of %s: the agents there get your text pushed into their sessions as messages from %s. Start again with --yes if that is what you want.\n", name, strings.Join(hit, ", "), name)
		return exitStatus(exitNeedsYes)
	}
	dead := make(chan struct{})
	t, err := openTunnel(ctx, o, g, dead, o.port)
	if err != nil {
		return err
	}
	defer t.stop()
	line, err := json.Marshal(tunnelReady{Event: "ready", Version: tunnelVersion(o, env), Group: group, Name: name, URL: t.url, ServerName: t.serverName, CAFile: t.caFile,
		SeedFile: g.keyOf(name), SettingsDir: dir, StateDir: g.stateDir, ObjectsFileMax: g.objectsFileMax(), Server: t.server, PID: os.Getpid()})
	if err != nil {
		return err
	}
	if len(line) > maxReadyLine {
		return errors.New("viper nats tunnel: the line ready is too long")
	}
	if _, err := fmt.Fprintf(stdout, "%s\n", line); err != nil {
		return err
	}
	// the program that started this one is gone when its end of stdin closes (a pipe does not outlive it)
	eof := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, stdin); close(eof) }()
	select {
	case <-ctx.Done():
		return nil
	case <-eof:
		return nil
	case <-dead:
		return errors.New("the ssh tunnel to the NATS server ended")
	}
}

// tunnelVersion is the version of this viper, which the MCP server compares with its own.
func tunnelVersion(o *options, env settings.Env) string {
	if o.version != "" {
		return o.version
	}
	return env.Version
}

// quoteArg quotes a word for the shell the user types the command in.
func quoteArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'$`\\&|;<>()*?[]{}#~!") {
		return s
	}
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// runNatsPrintConfig prints how Claude Code is told about the MCP server of the group (docs/plan/nats-lokal.md §4.5). It opens nothing; what
// is missing (the identity, the program, --yes, ssh) is said below the entry.
func runNatsPrintConfig(o *options, env settings.Env, group string, stdout io.Writer) error {
	name := o.name
	if name == "" {
		name = DefaultLocalName
	}
	if err := model.CheckNATSName("group", group); err != nil {
		return err
	}
	if err := model.CheckNATSName("identity", name); err != nil {
		return err
	}
	dir, how, err := findGroupDir(o, env, group, name, true)
	if err != nil {
		return err
	}
	o.dir = dir
	g, err := loadNatsGroup(o, env, group)
	if err != nil {
		return err
	}
	self, err := invokedViper(o)
	if err != nil {
		return err
	}
	mcp, err := nextToViper(o)
	if err != nil {
		return err
	}
	args, err := serverArgs(o, group, name, self, dir)
	if err != nil {
		return err
	}
	server := "nats-" + group
	words := []string{"claude", "mcp", "add", server, "--", mcp}
	words = append(words, args...)
	for i, w := range words {
		words[i] = quoteArg(w)
	}
	fmt.Fprintf(stdout, "Entry for Claude Code (the settings of %s are in %s: %s):\n\n  %s\n\n", group, dir, how, strings.Join(words, " "))
	js, err := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{server: map[string]any{"command": mcp, "args": args}}}, "  ", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "or in the file .mcp.json of a project:\n\n  %s\n\n", js)
	if o.pushFrom != "" {
		fmt.Fprintf(stdout, "Pushed messages need the channel switch of Claude Code (a research preview; interactive sessions only):\n\n  claude --dangerously-load-development-channels server:%s\n\n", server)
	}
	printConfigNotes(o, g, name, mcp, stdout)
	return nil
}

// printConfigNotes says what has to be done before the entry works.
func printConfigNotes(o *options, g *natsGroup, name, mcp string, stdout io.Writer) {
	var notes []string
	if !exists(g.keyOf(name)) {
		notes = append(notes, fmt.Sprintf("this computer has no key for %s: run viper nats init %s --name %s", name, g.name, name))
	}
	if !exists(mcp) {
		notes = append(notes, fmt.Sprintf("the program is not there yet: run viper mcp-binary --host (it writes %s)", quoteArg(mcp)))
	}
	if _, err := o.host().LookPath("ssh"); err != nil {
		notes = append(notes, "ssh was not found: the tunnel needs OpenSSH (on Windows an optional feature of the system)")
	}
	if hit := g.pushTargets(name); len(hit) > 0 {
		notes = append(notes, fmt.Sprintf("%s is in NATS_MCP_PUSH_FROM of %s: the agents there get the text of this session pushed into their sessions. The entry has no --yes; add it to the entry if that is what you want", name, strings.Join(hit, ", ")))
	}
	if len(notes) == 0 {
		return
	}
	fmt.Fprintln(stdout, "Before it works:")
	for _, n := range notes {
		fmt.Fprintf(stdout, "  - %s\n", n)
	}
}

// serverArgs are the words after viper-nats-mcp in the entry for Claude Code.
func serverArgs(o *options, group, name, viper, dir string) ([]string, error) {
	args := []string{"--via-viper", group, "--viper", viper, "--dir", dir}
	if name != DefaultLocalName {
		args = append(args, "--name", name)
	}
	if o.pushFrom != "" {
		args = append(args, "--push-from", o.pushFrom)
	}
	if o.files != "" {
		abs, err := filepath.Abs(o.files)
		if err != nil {
			return nil, err
		}
		args = append(args, "--files", abs)
	}
	return args, nil
}
