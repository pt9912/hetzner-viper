package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	hetznerviper "github.com/pt9912/hetzner-viper"
	"github.com/pt9912/hetzner-viper/internal/config"
	"github.com/pt9912/hetzner-viper/internal/runlog"
)

// The commands run on this computer: they need the files of the settings directory (--dir, default
// the current directory) and the OpenSSH client.

// need checks that a program the command runs on this computer exists, before anything is
// changed, and says what to do if not.
func (h *host) need(program string) error {
	if _, err := h.lookPath(program); err != nil {
		return fmt.Errorf("%s not found: install the OpenSSH client", program)
	}
	return nil
}

func hostEnv() (config.Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return config.Env{}, err
	}
	return config.Env{Lookup: os.LookupEnv, Home: home}, nil
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
func ensureKeys(h *host, key, ca, home string) error {
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
		if err := h.runTool("ssh-keygen", "-t", "ed25519", "-f", key, "-N", ""); err != nil {
			return err
		}
	}
	if !exists(ca) {
		if err := h.runTool("ssh-keygen", "-t", "ed25519", "-f", ca, "-N", "", "-C", "Hetzner Claude Host CA"); err != nil {
			return err
		}
	}
	return nil
}

// setup creates the login key and the host CA and trusts the CA for the domain .
func setup(o *options, env config.Env, out io.Writer) error {
	dir := settingsDir(o)
	file := filepath.Join(dir, ".env.local")
	if err := createEnvFile(file, out); err != nil {
		return err
	}
	if err := createMachineExample(dir, out); err != nil {
		return err
	}
	vars, err := config.LoadFile(dir, env)
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
	if err := config.CheckDomain(domain); err != nil {
		return err
	}
	key := config.KeyPath(get("SSH_KEY"), dir, env.Home)
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
  export HCLOUD_TOKEN='...'
  export CLOUDFLARE_API_TOKEN='...'
  export CLOUDFLARE_ZONE_ID='...'
Then run viper up <machine> [--repo URL]
Host CA public key for iPad SSH clients (only if they support OpenSSH host CAs):
%s
`, pubKey)
	return nil
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// connect opens a shell or runs one command on a machine, with the machine's own ssh config
// (pinned host key). With a command ssh gets a terminal only if we have one.
func connect(o *options, env config.Env, interactive bool) error {
	c, err := config.Load(settingsDir(o), o.machine, env, false)
	if err != nil {
		return err
	}
	if !exists(c.SSHConfig) {
		return fmt.Errorf("No SSH config for %s: run viper up %s first", c.ServerName, o.machine)
	}
	var args []string
	if o.cmd != "" && interactive {
		args = append(args, "-t")
	}
	args = append(args, "-F", c.SSHConfig, c.SSHAlias)
	if o.cmd != "" {
		args = append(args, o.cmd)
	}
	if err := o.host().need("ssh"); err != nil {
		return err
	}
	// ssh takes our place (like exec): signals and the exit code are its own
	return o.host().execTool("ssh", args...)
}

// dnsName prints the DNS name of a machine: <SERVER_PREFIX>-<machine>.<DNS_DOMAIN>.
func dnsName(o *options, env config.Env, out io.Writer) error {
	c, err := config.Load(settingsDir(o), o.machine, env, false)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, c.DNSHost)
	return nil
}

// showLog shows the events of all machines or one machine, or its complete output.
func showLog(o *options, env config.Env, out io.Writer) error {
	home := env.Home
	state := filepath.Join(home, ".local", "state", "hetzner-claude")
	server := ""
	if o.machine != "" {
		server = config.Prefix(settingsDir(o), o.machine, env) + "-" + o.machine
	}
	return runlog.Show(state, server, o.full, out)
}

func nativeLog(o *options) error {
	env, err := hostEnv()
	if err != nil {
		return err
	}
	return showLog(o, env, os.Stdout)
}

func nativeSetup(o *options) error {
	env, err := hostEnv()
	if err != nil {
		return err
	}
	return setup(o, env, os.Stdout)
}

func nativeSSH(o *options) error {
	env, err := hostEnv()
	if err != nil {
		return err
	}
	return connect(o, env, isTerminal(os.Stdin) && isTerminal(os.Stdout))
}

func nativeDNS(o *options) error {
	env, err := hostEnv()
	if err != nil {
		return err
	}
	return dnsName(o, env, os.Stdout)
}
