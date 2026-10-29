// Package sshconfig keeps the local ssh side of the machines in files (the adapter of
// internal/port/driven/sshconfig).
package sshconfig

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
)

// Files is the ssh side of the machines in ~/.ssh and the state directory.
type Files struct{}

// WriteConfig writes the machine's own ssh config (write_ssh_config in common.sh). Connect by
// IP (the exact server we act on, independent of DNS). Trust only the host key pinned for this
// very server: a key leaked from an earlier server (it is readable in the user-data) is
// rejected, even though its certificate from the same CA is still valid. Without a pin
// (server created elsewhere) it falls back to the host CA.
func (Files) WriteConfig(c *model.Machine, ip string) error {
	known := c.KnownPin
	if st, err := os.Stat(c.KnownPin); err != nil || st.Size() == 0 {
		known = filepath.Join(c.SSHDir, "known_hosts_hetzner_claude_ca")
	}
	body := fmt.Sprintf(`Host %s
    HostName %s
    HostKeyAlias %s
    User builder
    IdentityFile %s
    IdentitiesOnly yes
    StrictHostKeyChecking yes
    CheckHostIP no
    UserKnownHostsFile %s
    HostKeyAlgorithms ssh-ed25519-cert-v01@openssh.com,ssh-ed25519
    ServerAliveInterval 15
    ServerAliveCountMax 4
    # the server is root-equivalent for the agent: never hand it the local ssh-agent or X11
    ForwardAgent no
    ForwardX11 no
`, c.SSHAlias, ip, c.DNSHost, quote(c.SSHKey), quote(known))
	if err := os.WriteFile(c.SSHConfig, []byte(body), 0o600); err != nil {
		return err
	}
	return os.Chmod(c.SSHConfig, 0o600)
}

// ForgetStalePin drops a pinned host key recorded for another server ID: the name was reused by
// a new server, e.g. created from another computer.
func (Files) ForgetStalePin(c *model.Machine, id string, out io.Writer) {
	b, err := os.ReadFile(c.IDState)
	recorded := strings.TrimRight(string(b), "\n")
	if err != nil || recorded == "" || recorded == id {
		return
	}
	if _, err := os.Stat(c.KnownPin); err != nil {
		return
	}
	fmt.Fprintf(out, "Server %s was recreated (ID %s -> %s); dropping its old pinned host key.\n", c.ServerName, recorded, id)
	_ = os.Remove(c.KnownPin) // best effort: a stale pin that stays is reported by the next ssh
}

// PinHostKey pins the host key of the server: without a pin, the connection was verified through the host CA; record the
// server's host key, read over that verified channel, as the pin. It does nothing if the key
// cannot be read.
func (Files) PinHostKey(c *model.Machine, id string, ssh func(args ...string) (string, error), out io.Writer) {
	if st, err := os.Stat(c.KnownPin); err == nil && st.Size() > 0 {
		return
	}
	stdout, err := ssh("-n", "-F", c.SSHConfig, "-o", "BatchMode=yes", c.SSHAlias, "cat /etc/ssh/ssh_host_ed25519_key.pub")
	f := strings.Fields(strings.SplitN(stdout, "\n", 2)[0])
	if err != nil || len(f) < 2 || f[0] != "ssh-ed25519" {
		return
	}
	if err := os.WriteFile(c.KnownPin, []byte(c.DNSHost+" "+f[0]+" "+f[1]+"\n"), 0o600); err != nil {
		return
	}
	_ = os.Chmod(c.KnownPin, 0o600)                     // the file was just written with 600; this covers an older, looser one
	_ = os.WriteFile(c.IDState, []byte(id+"\n"), 0o600) // best effort: without it the next run asks the API
	fmt.Fprintf(out, "Pinned host key of %s (verified via host CA).\n", c.ServerName)
}

// AddInclude adds the line 'Include <this machine's config>' to ~/.ssh/config.
func (Files) AddInclude(c *model.Machine) error { return include(c, true) }

// RemoveInclude removes that line again.
func (Files) RemoveInclude(c *model.Machine) error { return include(c, false) }

// include adds or removes the line 'Include <this machine's config>' in ~/.ssh/config, in
// place (a symlink into a dotfiles repository stays), serialized with a lock so that parallel
// runs do not lose each other's line. An error means the file cannot be edited: the commands
// themselves use -F, the Include only serves plain `ssh <alias>` and VS Code.
func include(c *model.Machine, add bool) error {
	cfg, err := filepath.EvalSymlinks(filepath.Join(c.SSHDir, "config"))
	if err != nil {
		cfg = filepath.Join(c.SSHDir, "config")
		f, cerr := os.OpenFile(cfg, os.O_CREATE|os.O_WRONLY, 0o600)
		if cerr != nil {
			return cerr
		}
		_ = f.Close() // only created
		if cfg, err = filepath.EvalSymlinks(cfg); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(cfg, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_ = f.Close() // only opened to see that it is writable
	unlock, err := lockFile(filepath.Join(c.StateDir, "ssh-config.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	b, err := os.ReadFile(cfg)
	if err != nil {
		return err
	}
	line := "Include " + quote(c.SSHConfig)
	isLine := includeMatcher(c)
	var out string
	switch {
	case add:
		for _, l := range strings.Split(string(b), "\n") {
			if isLine(l) {
				return os.Chmod(cfg, 0o600)
			}
		}
		out = line + "\n" + string(b)
	default:
		var kept []string
		for _, l := range strings.SplitAfter(string(b), "\n") {
			if !isLine(l) {
				kept = append(kept, l)
			}
		}
		out = strings.Join(kept, "")
	}
	if err := os.WriteFile(cfg, []byte(out), 0o600); err != nil {
		return err
	}
	return os.Chmod(cfg, 0o600)
}

// IncludeWarning is what to tell the user when SSHInclude failed.
func (Files) IncludeWarning(c *model.Machine) string {
	target, _ := filepath.EvalSymlinks(filepath.Join(c.SSHDir, "config"))
	return fmt.Sprintf("WARNING: could not edit ~/.ssh/config (%s); add or remove the line 'Include %s' yourself.", target, c.SSHConfig)
}

// quote puts a path with white space (C:\Users\Jane Doe\.ssh) in double quotes, as ssh_config needs.
func quote(path string) string {
	if strings.ContainsAny(path, " \t") {
		return `"` + path + `"`
	}
	return path
}

// includeMatcher recognises the Include line of a machine, as an earlier version wrote it (no
// quotes) and with the CRLF of a Windows file.
func includeMatcher(c *model.Machine) func(string) bool {
	return func(l string) bool {
		l = strings.TrimRight(l, "\r\n")
		return l == "Include "+quote(c.SSHConfig) || l == "Include "+c.SSHConfig
	}
}
