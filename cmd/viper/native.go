package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	hetznerviper "github.com/pt9912/hetzner-viper"
	"github.com/pt9912/hetzner-viper/internal/config"
	"github.com/pt9912/hetzner-viper/internal/runlog"
)

// The commands run on this computer: they need the files of the settings directory (--dir, default
// the current directory) and the OpenSSH client.

// replaced by the tests
var (
	runTool  = runChild
	execTool = replaceProcess
	lookPath = exec.LookPath
)

// need checks that a program the command runs on this computer exists, before anything is
// changed, and says what to do if not.
func need(program string) error {
	if _, err := lookPath(program); err != nil {
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

// setup creates the login key and the host CA and trusts the CA for the domain (setup.sh).
func setup(o *options, env config.Env, out io.Writer) error {
	dir := settingsDir(o)
	file := filepath.Join(dir, ".env.local")
	// O_EXCL: two runs at once do not truncate each other's file; 600: it holds API tokens
	switch f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); {
	case err == nil:
		_, werr := f.Write(hetznerviper.EnvExample)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
		fmt.Fprintf(out, "Created %s from the template; set DNS_DOMAIN in it.\n", file)
	case !os.IsExist(err):
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
	// before anything is created: without OpenSSH a key cannot be made
	if !exists(key) || !exists(ca) {
		if err := need("ssh-keygen"); err != nil {
			return err
		}
	}
	for _, d := range []string{filepath.Dir(key), filepath.Join(env.Home, ".ssh")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	if !exists(key) {
		if err := runTool("ssh-keygen", "-t", "ed25519", "-f", key, "-N", ""); err != nil {
			return err
		}
	}
	if !exists(ca) {
		if err := runTool("ssh-keygen", "-t", "ed25519", "-f", ca, "-N", "", "-C", "Hetzner Claude Host CA"); err != nil {
			return err
		}
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
// (pinned host key) (connect.sh). With a command ssh gets a terminal only if we have one.
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
	if err := need("ssh"); err != nil {
		return err
	}
	// ssh takes our place (like connect.sh's exec): signals and the exit code are its own
	return execTool("ssh", args...)
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

// showLog is make log: the events of all machines or one machine, or its complete output.
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
