package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/web"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/natsmcp"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

// nativeUI is viper ui: the web server on 127.0.0.1 with the pages of docs/plan/ui.md.
func nativeUI(o *options) error {
	h := o.host()
	env, err := o.env()
	if err != nil {
		return err
	}
	secrets := &secretValues{}
	b := &uiBackend{o: o, env: env, secrets: secrets}
	b.learnEnvironment()
	cfg := web.Config{Backend: b, Readonly: o.readonly, Version: o.version, Clean: natsmcp.Clean, Redact: secrets.redact}
	if !o.readonly {
		cfg.Spawn = spawner(settingsDir(o))
	}
	if o.noOpen {
		cfg.TokenTTL = 10 * time.Minute // the address is copied by hand
	}
	srv := web.New(cfg)
	ln, err := srv.Listen(o.port)
	if err != nil {
		if o.port != 0 {
			return fmt.Errorf("viper ui: cannot listen on 127.0.0.1:%d (%v); --port chooses another, without it a free one is taken", o.port, err)
		}
		return fmt.Errorf("viper ui: cannot listen: %w", err)
	}
	if os.Geteuid() == 0 {
		fmt.Fprintln(h.Stderr, "viper ui: warning: you are root; the pages can run viper checks as root. Start it as your own user.")
	}
	ctx, stop := notifyContext(context.Background())
	defer stop()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	fmt.Fprintf(h.Stdout, "viper ui: http://%s/ (Ctrl-C ends it)\n", srv.Host())
	if o.readonly {
		fmt.Fprintln(h.Stdout, "viper ui: read only (no preflight, up or down)")
	}
	if o.noOpen {
		fmt.Fprintf(h.Stdout, "viper ui: open this address in your browser within %d minutes (it works once): %s\n", int(cfg.TokenTTL.Minutes()), srv.URL())
	} else if dir := openStart(h, srv); dir != "" {
		defer func() { _ = os.RemoveAll(dir) }()
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		fmt.Fprintln(h.Stdout, "viper ui: ending")
		return <-done
	}
}

// openStart opens the browser on a file that sends it to the address with the key, so that the key is not in the
// command line of a program (ps shows that to other users). The file is only for this user; the directory it is in is
// returned, and the caller removes it when the server ends.
func openStart(h *Host, srv *web.Server) string {
	dir, err := os.MkdirTemp("", "viper-ui-")
	if err == nil {
		file := filepath.Join(dir, "start.html")
		page := `<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=` + srv.URL() + `"><title>viper</title>`
		if err = os.WriteFile(file, []byte(page), 0o600); err == nil {
			name, args := browserCommand(runtime.GOOS, file)
			_, err = h.OutputTool(name, args...)
		}
	}
	if err != nil {
		fmt.Fprintf(h.Stderr, "viper ui: could not open the browser (%v); open this address within a minute: %s\n", err, srv.URL())
	}
	return dir
}

// browserCommand is the program that opens a file in the default program of the system. Never cmd /c start: it reads the
// & in an address as the end of the command.
func browserCommand(goos, file string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{file}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", file}
	}
	return "xdg-open", []string{file}
}

// uiBackend answers the pages with the commands of viper, called in this process.
type uiBackend struct {
	o       *options
	env     settings.Env
	secrets *secretValues
}

func (b *uiBackend) dir() string { return settingsDir(b.o) }

// Overview lists the machines of machines/*.env.local and of groups/*.env, with what the settings and the marks of this
// computer say. It asks no provider.
func (b *uiBackend) Overview(ctx context.Context) (web.Overview, error) {
	ov := web.Overview{Machines: []web.Machine{}, Groups: []web.Group{}}
	inGroup := map[string]bool{}
	for _, name := range listNames(filepath.Join(b.dir(), "groups"), ".env") {
		e := b.env
		e.Group = name
		g, err := b.o.host().Services.Settings.LoadGroup(b.dir(), name, e)
		if err != nil {
			ov.Groups = append(ov.Groups, web.Group{Name: name, Error: err.Error()})
			continue
		}
		ov.Groups = append(ov.Groups, web.Group{Name: name, Machines: g.Machines})
		for _, m := range g.Machines {
			inGroup[m] = true
			ov.Machines = append(ov.Machines, b.machine(m, name))
		}
	}
	for _, name := range listNames(filepath.Join(b.dir(), "machines"), ".env.local") {
		if !inGroup[name] {
			ov.Machines = append(ov.Machines, b.machine(name, ""))
		}
	}
	ov.NATS = b.natsGroups(ov)
	sort.SliceStable(ov.Machines, func(i, j int) bool {
		a, c := ov.Machines[i], ov.Machines[j]
		return a.Name < c.Name || a.Name == c.Name && a.Group < c.Group
	})
	return ov, ctx.Err()
}

// natsGroups are the groups of groups/*.env whose machines are in a NATS group, and whether this computer has the key of the
// operator (then viper nats speaks as the operator).
func (b *uiBackend) natsGroups(ov web.Overview) []web.NATSGroup {
	state := model.StateDir(b.env.Home)
	out := []web.NATSGroup{}
	for _, g := range ov.Groups {
		if g.Error != "" {
			continue
		}
		on := false
		for _, m := range ov.Machines {
			if m.Group == g.Name && m.NATS != "" {
				on = true
			}
		}
		if on {
			out = append(out, web.NATSGroup{Name: g.Name, Machines: g.Machines, Operator: hasOperatorKey(state, g.Name)})
		}
	}
	return out
}

// hasOperatorKey looks for the key at its place (and at the one an older version used); it moves nothing.
func hasOperatorKey(state, group string) bool {
	return exists(filepath.Join(state, "groups", group, "operator.nk")) || exists(filepath.Join(state, "nats", group, "operator.nk"))
}

// machine loads the settings of one machine without asking for the credentials and reports what is there.
func (b *uiBackend) machine(name, group string) web.Machine {
	e := b.env
	e.Group = group
	m := web.Machine{Name: name, Group: group}
	c, err := b.o.host().Services.Settings.Load(b.dir(), name, e, false)
	if err != nil {
		m.Error = err.Error()
		return m
	}
	b.secrets.learn(c)
	m.Provider, m.Type, m.Location = c.Provider, c.ServerType, c.ServerLocation
	if c.NATS.Enabled {
		m.NATS = c.NATS.Group
	}
	m.State = markState(c)
	return m
}

// markState is what the marks of this computer say: ready (viper up ended), started (an id, no end), none.
func markState(c *model.Machine) string {
	switch {
	case exists(c.ReadyState):
		return "ready"
	case exists(c.IDState):
		return "started"
	}
	return "none"
}

// listNames are the names of the files of dir with the suffix, without it, in order.
func listNames(dir, suffix string) []string {
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		if n, ok := strings.CutSuffix(e.Name(), suffix); ok && !e.IsDir() && n != "" && !strings.HasPrefix(n, ".") && !strings.HasPrefix(n, "example") {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// Check is viper check for one machine, logged as at the terminal.
func (b *uiBackend) Check(ctx context.Context, machine, group string, out io.Writer) (int, error) {
	if err := b.o.host().need("ssh"); err != nil {
		return 1, err
	}
	co := *b.o
	co.machine, co.group, co.within = machine, "", group
	e := b.env
	e.Group = group
	err := runCheck(ctx, &co, e, out, out)
	return exitCode(err), nilIfStatus(err)
}

// nilIfStatus drops an exit status: the code says it, and the output has the reason.
func nilIfStatus(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(exitStatus); ok {
		return nil
	}
	return err
}

// Log is viper log.
func (b *uiBackend) Log(_ context.Context, machine string, full bool, out io.Writer) error {
	lo := *b.o
	lo.machine, lo.full = machine, full
	return showLog(&lo, b.env, out)
}

// secretValues are the values of the settings that are secrets, which no output of the pages may show.
type secretValues struct {
	mu   sync.Mutex
	vals map[string]bool
}

// secretNames are the settings whose values are credentials: those of the providers and the ones viper puts on the
// machine (editor/vscode.go keeps the first kind from VS Code the same way).
func secretNames() []string {
	names := []string{"CLOUDFLARE_API_TOKEN", "VIPER_GH_TOKEN", "GH_TOKEN", "VIPER_CLAUDE_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "VIPER_ANTHROPIC_AUTH_TOKEN", "VIPER_CODE_SERVER_PASSWORD"}
	for _, p := range model.ProviderNames() {
		if info, ok := model.LookupProvider(p); ok {
			names = append(names, info.Secrets...)
		}
	}
	return names
}

// minSecret is the shortest value that is taken out of an output: a shorter one would cut up ordinary words.
const minSecret = 8

func (s *secretValues) add(v string) {
	if len(v) < minSecret {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.vals == nil {
		s.vals = map[string]bool{}
	}
	s.vals[v] = true
}

func (s *secretValues) learn(c *model.Machine) {
	for _, n := range secretNames() {
		s.add(c.Get(n))
	}
}

// learnEnvironment takes the secrets of the environment and of .env.local.
func (b *uiBackend) learnEnvironment() {
	for _, n := range secretNames() {
		b.secrets.add(b.env.Getenv(n))
	}
	if vars, err := b.o.host().Services.Settings.LoadFile(b.dir(), b.env); err == nil {
		for _, n := range secretNames() {
			b.secrets.add(vars[n])
		}
	}
}

func (s *secretValues) redact(text string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for v := range s.vals {
		text = strings.ReplaceAll(text, v, "[hidden]")
	}
	return text
}
