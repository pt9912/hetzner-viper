package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	hetznerviper "github.com/pt9912/hetzner-viper"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

// The commands run on this computer: they need the files of the settings directory (--dir, default
// the current directory) and the OpenSSH client.

// need checks that a program the command runs on this computer exists, before anything is
// changed, and says what to do if not.
func (h *Host) need(program string) error {
	if _, err := h.LookPath(program); err != nil {
		return fmt.Errorf("%s not found: install the OpenSSH client", program)
	}
	return nil
}

func settingsDir(o *options) string {
	if o.dir != "" {
		return o.dir
	}
	return "."
}

// createEnvFile writes .env.local from the template unless it already exists.
func createEnvFile(file string, out io.Writer) error {
	// 600: it holds API tokens
	return writeTemplate(file, hetznerviper.EnvExample, 0o600, "Created %s from the template; set DNS_DOMAIN in it.\n", out)
}

// createMachineExample writes machines/example.env.local.example unless it already exists: the
// template for the settings of one machine, to copy to machines/<machine>.env.local.
func createMachineExample(dir string, out io.Writer) error {
	if err := os.MkdirAll(filepath.Join(dir, "machines"), 0o755); err != nil {
		return err
	}
	file := filepath.Join(dir, "machines", "example.env.local.example")
	return writeTemplate(file, hetznerviper.MachineExample, 0o644, "Created %s: copy it to machines/<machine>.env.local for the settings of one machine.\n", out)
}

// writeTemplate writes a template to a file that does not exist yet and says so; O_EXCL: two runs
// at once do not truncate each other's file.
func writeTemplate(file string, data []byte, mode os.FileMode, message string, out io.Writer) error {
	switch f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode); {
	case err == nil:
		_, werr := f.Write(data)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
		fmt.Fprintf(out, message, file)
	case !os.IsExist(err):
		return err
	}
	return nil
}

// ensureKeys creates the login key and the host CA key if they are missing.
func ensureKeys(h *Host, key, ca, home string) error {
	// before anything is created: without OpenSSH a key cannot be made
	if !exists(key) || !exists(ca) {
		if err := h.need("ssh-keygen"); err != nil {
			return err
		}
	}
	for _, d := range []string{filepath.Dir(key), filepath.Join(home, ".ssh")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	if !exists(key) {
		if err := h.RunTool("ssh-keygen", "-t", "ed25519", "-f", key, "-N", ""); err != nil {
			return err
		}
	}
	if !exists(ca) {
		if err := h.RunTool("ssh-keygen", "-t", "ed25519", "-f", ca, "-N", "", "-C", "Hetzner Claude Host CA"); err != nil {
			return err
		}
	}
	return nil
}

// setup creates the login key and the host CA and trusts the CA for the domain .
func setup(o *options, env settings.Env, out io.Writer) error {
	dir := settingsDir(o)
	file := filepath.Join(dir, ".env.local")
	if err := createEnvFile(file, out); err != nil {
		return err
	}
	if err := createMachineExample(dir, out); err != nil {
		return err
	}
	vars, err := o.host().Services.Settings.LoadFile(dir, env)
	if err != nil {
		return err
	}
	get := func(k string) string {
		if v, ok := vars[k]; ok {
			return v
		}
		return env.Getenv(k)
	}
	domain := get("DNS_DOMAIN")
	if domain == "" {
		return fmt.Errorf("Set DNS_DOMAIN in .env.local")
	}
	if err := model.CheckDomain(domain); err != nil {
		return err
	}
	key := model.KeyPath(get("SSH_KEY"), dir, env.Home)
	ca := filepath.Join(env.Home, ".ssh", "hetzner_claude_host_ca")
	if err := ensureKeys(o.host(), key, ca, env.Home); err != nil {
		return err
	}
	pub, err := os.ReadFile(ca + ".pub")
	if err != nil {
		return err
	}
	pubKey := strings.TrimRight(string(pub), "\n")
	known := filepath.Join(env.Home, ".ssh", "known_hosts_hetzner_claude_ca")
	if err := os.WriteFile(known, []byte(fmt.Sprintf("@cert-authority *.%s %s\n", domain, pubKey)), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(known, 0o600); err != nil {
		return err
	}
	fmt.Fprintf(out, `Setup complete. Export credentials in your shell:
%sThen run viper up <machine> [--repo URL]
Host CA public key for iPad SSH clients (only if they support OpenSSH host CAs):
%s
`, exportLines(o.host().Services.Settings.ProviderOf(vars)), pubKey)
	return nil
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// tmuxCommand attaches to the tmux session "claude" of the machine, the one the terminals of VS Code
// use, or creates it in the work directory (a command over ssh does not start in it by itself).
const tmuxCommand = `cd "$(cat ~/.config/viper/workdir 2>/dev/null)" 2>/dev/null; exec tmux new-session -A -s claude`

// connect opens a shell or runs one command on a machine, with the machine's own ssh config
// (pinned host key). With a command ssh gets a terminal only if we have one.
func connect(o *options, env settings.Env, interactive bool) error {
	c, err := o.host().Services.Settings.Load(settingsDir(o), o.machine, env, false)
	if err != nil {
		return err
	}
	if !exists(c.SSHConfig) {
		return fmt.Errorf("No SSH config for %s: run viper up %s first", c.ServerName, o.machine)
	}
	command := o.cmd
	if o.tmux {
		if o.cmd != "" {
			return fmt.Errorf("viper ssh: --tmux and --cmd exclude each other")
		}
		if !interactive {
			return fmt.Errorf("viper ssh: --tmux needs a terminal")
		}
		command = tmuxCommand
	}
	var args []string
	if command != "" && interactive {
		args = append(args, "-t")
	}
	args = append(args, "-F", c.SSHConfig, c.SSHAlias)
	if command != "" {
		args = append(args, command)
	}
	if err := o.host().need("ssh"); err != nil {
		return err
	}
	// ssh takes our place (like exec): signals and the exit code are its own
	return o.host().ExecTool("ssh", args...)
}

// dnsName prints the DNS name of a machine: <SERVER_PREFIX>-<machine>.<DNS_DOMAIN>.
func dnsName(o *options, env settings.Env, out io.Writer) error {
	c, err := o.host().Services.Settings.Load(settingsDir(o), o.machine, env, false)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, c.DNSHost)
	return nil
}

// showLog shows the events of all machines or one machine, or its complete output.
func showLog(o *options, env settings.Env, out io.Writer) error {
	home := env.Home
	state := filepath.Join(home, ".local", "state", "hetzner-claude")
	server := ""
	if o.machine != "" {
		server = o.host().Services.Settings.Prefix(settingsDir(o), o.machine, env) + "-" + o.machine
	}
	return o.host().Services.ShowLog(state, server, o.full, out)
}

func nativeLog(o *options) error {
	env, err := o.host().Env()
	if err != nil {
		return err
	}
	return showLog(o, env, o.host().Stdout)
}

func nativeSetup(o *options) error {
	env, err := o.host().Env()
	if err != nil {
		return err
	}
	return setup(o, env, o.host().Stdout)
}

func nativeSSH(o *options) error {
	env, err := o.host().Env()
	if err != nil {
		return err
	}
	return connect(o, env, o.host().IsTerminal())
}

func nativeDNS(o *options) error {
	env, err := o.host().Env()
	if err != nil {
		return err
	}
	return dnsName(o, env, o.host().Stdout)
}

// exportLines are the credentials of the provider and of Cloudflare, as lines to paste.
func exportLines(provider string) string {
	prov, _ := model.LookupProvider(provider)
	var b strings.Builder
	for _, name := range append(append([]string{}, prov.Credentials...), "CLOUDFLARE_API_TOKEN", "CLOUDFLARE_ZONE_ID") {
		fmt.Fprintf(&b, "  export %s='...'\n", name)
	}
	return b.String()
}
