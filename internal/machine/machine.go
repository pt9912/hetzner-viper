// Package machine holds what several commands share about a machine's local side: the ssh
// configuration the scripts write, and the working directory on the server.
package machine

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/config"
)

// WriteSSHConfig writes the machine's own ssh config (write_ssh_config in common.sh). Connect by
// IP (the exact server we act on, independent of DNS). Trust only the host key pinned for this
// very server: a key leaked from an earlier server (it is readable in the user-data) is
// rejected, even though its certificate from the same CA is still valid. Without a pin
// (server created elsewhere) it falls back to the host CA.
func WriteSSHConfig(c *config.Config, ip string) error {
	known := c.KnownPin
	if st, err := os.Stat(c.KnownPin); err != nil || st.Size() == 0 {
		known = c.SSHDir + "/known_hosts_hetzner_claude_ca"
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
`, c.SSHAlias, ip, c.DNSHost, c.SSHKey, known)
	if err := os.WriteFile(c.SSHConfig, []byte(body), 0o600); err != nil {
		return err
	}
	return os.Chmod(c.SSHConfig, 0o600)
}

// WorkDir is where the repository is on the server: the workspace directory, and below it the
// repository's name (the last part of its URL without .git).
func WorkDir(c *config.Config, repoURL string) string {
	if repoURL == "" {
		return c.WorkspaceDir
	}
	return c.WorkspaceDir + "/" + path.Base(strings.TrimSuffix(repoURL, ".git"))
}

// Quote quotes a word for the remote shell: one argument, whatever it contains.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// QuoteAll quotes the words and joins them with spaces.
func QuoteAll(words ...string) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = Quote(w)
	}
	return strings.Join(q, " ")
}

var (
	hostKeyRe = regexp.MustCompile(`(?i)host key verification failed|remote host identification has changed|host key for .* has changed|no .* host key is known|certificate invalid`)
	authRe    = regexp.MustCompile(`(?i)permission denied`)
)

// SSHFailureKind says why ssh failed, from its error output: "hostkey" if it rejected the
// server's identity, "auth" if the server rejected our key, else "network".
func SSHFailureKind(stderr string) string {
	switch {
	case hostKeyRe.MatchString(stderr):
		return "hostkey"
	case authRe.MatchString(stderr):
		return "auth"
	}
	return "network"
}

// ForgetStalePin drops a pinned host key recorded for another server ID: the name was reused by
// a new server, e.g. created from another computer.
func ForgetStalePin(c *config.Config, id string, out io.Writer) {
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

// PinFromServer pins the host key of the server: without a pin, the connection was verified through the host CA; record the
// server's host key, read over that verified channel, as the pin. It does nothing if the key
// cannot be read.
func PinFromServer(c *config.Config, id string, ssh func(args ...string) (string, error), out io.Writer) {
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

// SSHInclude adds or removes the line 'Include <this machine's config>' in ~/.ssh/config, in
// place (a symlink into a dotfiles repository stays), serialized with a lock so that parallel
// runs do not lose each other's line. An error means the file cannot be edited: the commands
// themselves use -F, the Include only serves plain `ssh <alias>` and VS Code.
func SSHInclude(c *config.Config, action string) error {
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
	line := "Include " + c.SSHConfig
	var out string
	switch action {
	case "add":
		for _, l := range strings.Split(string(b), "\n") {
			if l == line {
				return os.Chmod(cfg, 0o600)
			}
		}
		out = line + "\n" + string(b)
	default:
		var kept []string
		for _, l := range strings.SplitAfter(string(b), "\n") {
			if strings.TrimSuffix(l, "\n") != line {
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

// SSHIncludeWarning is what to tell the user when SSHInclude failed.
func SSHIncludeWarning(c *config.Config) string {
	target, _ := filepath.EvalSymlinks(filepath.Join(c.SSHDir, "config"))
	return fmt.Sprintf("WARNING: could not edit ~/.ssh/config (%s); add or remove the line 'Include %s' yourself.", target, c.SSHConfig)
}
