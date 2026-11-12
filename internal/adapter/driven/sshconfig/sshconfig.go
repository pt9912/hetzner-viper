// Package sshconfig keeps the local ssh side of the machines in files (the adapter of
// internal/port/driven/sshconfig).
package sshconfig

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
)

// Files is the ssh side of the machines in ~/.ssh and the state directory.
type Files struct{}

// WriteConfig writes the machine's own ssh config (write_ssh_config in common.sh). Connect by
// IP (the exact server we act on, independent of DNS). Trust only the host key pinned for this
// very server: a key leaked from an earlier server (it is readable in the user-data) is
// rejected, even though its certificate from the same CA is still valid. Without a pin
// (server created elsewhere) it falls back to the host CA.
//
// A machine that is a container (PROVIDER=kubernetes) has no address: it is reached on port 2222 through
// "viper k8s-proxy", which connects ssh to the pod through the API of the cluster; ip is ignored.
func (Files) WriteConfig(c *model.Machine, ip string) error {
	known := c.KnownPin
	if st, err := os.Stat(c.KnownPin); err != nil || st.Size() == 0 {
		known = filepath.Join(c.SSHDir, "known_hosts_hetzner_claude_ca")
	}
	connect := fmt.Sprintf("    HostName %s\n", ip)
	if c.IsContainer() {
		proxy, err := proxyCommand(c)
		if err != nil {
			return err
		}
		connect = fmt.Sprintf("    HostName %s\n    Port 2222\n    ProxyCommand %s\n", c.ServerName, proxy)
	}
	body := fmt.Sprintf(`Host %s
%s    HostKeyAlias %s
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
`, c.SSHAlias, connect, c.DNSHost, quote(c.SSHKey), quote(known))
	return writeAtomic(c.SSHConfig, []byte(body))
}

// viperPath is where the ssh config finds this program. A package manager keeps the program in a versioned directory
// and links it into a stable place (Homebrew: Cellar and bin): the link is used if it leads to this program, so the
// config does not point to a directory that the next upgrade removes.
func viperPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("the path of viper (for the ProxyCommand): %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe, nil
	}
	if onPath, err := exec.LookPath("viper"); err == nil {
		if abs, err := filepath.Abs(onPath); err == nil {
			if r, err := filepath.EvalSymlinks(abs); err == nil && r == resolved {
				return abs, nil
			}
		}
	}
	return resolved, nil
}

// proxyCommand is the ProxyCommand of a container: this program, k8s-proxy, the settings directory and the machine.
// ssh expands % in it and splits it like a shell would, so the parts are quoted and % is doubled; names with a quote or
// a line break are refused (they could add directives to the file).
func proxyCommand(c *model.Machine) (string, error) {
	exe, err := viperPath()
	if err != nil {
		return "", err
	}
	dir, err := filepath.Abs(c.Dir)
	if err != nil {
		return "", err
	}
	// on Windows the paths have backslashes, which the quoting below would have to refuse; Windows takes slashes too
	parts := []string{filepath.ToSlash(exe), "k8s-proxy", "--dir", filepath.ToSlash(dir), c.Name}
	for i, p := range parts {
		if strings.ContainsAny(p, "\"\n\r\\'$`") {
			return "", fmt.Errorf("the ProxyCommand cannot hold %q (a quote, a backslash, a dollar sign, a backtick or a line break)", p)
		}
		p = strings.ReplaceAll(p, "%", "%%")
		parts[i] = `"` + p + `"`
	}
	return strings.Join(parts, " "), nil
}

// writeAtomic puts the file in place in one step (a file of its own next to it, then rename), so
// that a reader never sees it empty or half written: several runs of a group write the config of the
// server and read it with ssh -F at the same time. An unchanged file is not touched at all.
func writeAtomic(path string, data []byte) error {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target // a link into a dotfiles repository stays a link: the file it points to is replaced
	}
	removeOldTemp(path)
	if old, err := os.ReadFile(path); err == nil && string(old) == string(data) {
		return os.Chmod(path, 0o600)
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp, 0o600)
	}
	if werr == nil {
		werr = renameWithRetry(tmp, path)
	}
	if werr != nil {
		_ = os.Remove(tmp) // best effort: the error is the one returned
	}
	return werr
}

// renameWithRetry puts the file in place; Windows refuses it for a moment while another program has the old
// file open, so it tries for about a second.
func renameWithRetry(from, to string) error {
	var err error
	for i := 0; i < 50; i++ {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return err
}

// removeOldTemp deletes the temporary files of earlier writes that a crash left behind (older than an hour).
func removeOldTemp(path string) {
	old, _ := filepath.Glob(path + ".tmp*")
	for _, f := range old {
		if st, err := os.Stat(f); err == nil && time.Since(st.ModTime()) > time.Hour {
			_ = os.Remove(f) // best effort
		}
	}
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
	hostKey := "/etc/ssh/ssh_host_ed25519_key.pub"
	if c.IsContainer() {
		hostKey = "/home/builder/.viper/sshd/host_key.pub" // the host key of a container is in its home directory
	}
	stdout, err := ssh("-n", "-F", c.SSHConfig, "-o", "BatchMode=yes", c.SSHAlias, "cat "+hostKey)
	f := strings.Fields(strings.SplitN(stdout, "\n", 2)[0])
	if err != nil || len(f) < 2 || f[0] != "ssh-ed25519" {
		return
	}
	if err := writeAtomic(c.KnownPin, []byte(c.DNSHost+" "+f[0]+" "+f[1]+"\n")); err != nil {
		return
	}
	_ = writeAtomic(c.IDState, []byte(id+"\n")) // best effort: without it the next run asks the API
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
