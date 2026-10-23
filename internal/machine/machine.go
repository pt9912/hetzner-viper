// Package machine holds what several commands share about a machine's local side: the ssh
// configuration the scripts write, and the working directory on the server.
package machine

import (
	"fmt"
	"os"
	"path"
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
