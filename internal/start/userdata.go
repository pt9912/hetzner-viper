package start

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// needle is where the authorized keys go in cloud-init.yaml: right after the builder user.
const needle = "    lock_passwd: true\n"

// UserData makes the cloud-init user-data of a new server from cloud-init.yaml: the login key
// (and the iPad's, if any) for builder, and the host key with its certificate, which cloud-init's
// ssh module installs (it deletes /etc/ssh/ssh_host_* on first boot, so write_files would not do).
// JSON strings are valid YAML, so any character is quoted correctly.
func UserData(base []byte, authorizedKeys []string, hostPrivate, hostPublic, hostCert string) (string, error) {
	s := string(base)
	if strings.Count(s, needle) != 1 {
		return "", fmt.Errorf("cloud-init.yaml: expected exactly one %q", strings.TrimSpace(needle))
	}
	var keys strings.Builder
	keys.WriteString("    ssh_authorized_keys:\n")
	for _, k := range authorizedKeys {
		keys.WriteString("      - " + jsonString(strings.TrimSpace(k)) + "\n")
	}
	s = strings.Replace(s, needle, needle+keys.String(), 1)
	var extra strings.Builder
	extra.WriteString("\nssh_keys:\n  ed25519_private: |\n")
	for _, l := range strings.Split(strings.TrimRight(hostPrivate, "\n"), "\n") {
		extra.WriteString("    " + l + "\n")
	}
	extra.WriteString("  ed25519_public: " + jsonString(strings.TrimSpace(hostPublic)) + "\n")
	extra.WriteString("  ed25519_certificate: " + jsonString(strings.TrimSpace(hostCert)) + "\n")
	return s + extra.String(), nil
}

// jsonString is a string as Python's json.dumps writes it (ASCII only): quotes and backslashes
// escaped, the short forms for \n \r \t \b \f, every other character outside space..~ as \uXXXX
// (surrogate pairs above U+FFFF). Any JSON string is valid YAML.
func jsonString(s string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			out.WriteString(`\"`)
		case r == '\\':
			out.WriteString(`\\`)
		case r == '\n':
			out.WriteString(`\n`)
		case r == '\r':
			out.WriteString(`\r`)
		case r == '\t':
			out.WriteString(`\t`)
		case r == '\b':
			out.WriteString(`\b`)
		case r == '\f':
			out.WriteString(`\f`)
		case r >= ' ' && r <= '~':
			out.WriteRune(r)
		case r > 0xFFFF:
			r -= 0x10000
			fmt.Fprintf(&out, "\\u%04x\\u%04x", 0xD800+(r>>10), 0xDC00+(r&0x3FF))
		default:
			fmt.Fprintf(&out, "\\u%04x", r)
		}
	}
	out.WriteByte('"')
	return out.String()
}

// HostKey is a new SSH host key for a server and its certificate, signed by the host CA.
type HostKey struct {
	Private, Public, Certificate string
	KnownHostsLine               string // "<dns name> <type> <key>" for the pin
}

// MakeHostKey creates a host key and signs it with the CA for the DNS name, valid from five
// minutes ago to seven days ahead. -C: the default comment (local user@host) would end up in
// the user-data.
func MakeHostKey(run func(name string, args ...string) error, ca, serverName, dnsHost string, now time.Time) (*HostKey, error) {
	tmp, err := os.MkdirTemp("", "hostkey")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := os.Chmod(tmp, 0o700); err != nil {
		return nil, err
	}
	key := filepath.Join(tmp, "ssh_host_ed25519_key")
	if err := run("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", serverName, "-f", key); err != nil {
		return nil, err
	}
	if err := run("ssh-keygen", "-q", "-s", ca, "-I", serverName+"-"+strconv.FormatInt(now.Unix(), 10), "-h", "-n", dnsHost, "-V", "-5m:+7d", key+".pub"); err != nil {
		return nil, err
	}
	read := func(f string) (string, error) { b, err := os.ReadFile(f); return string(b), err }
	h := &HostKey{}
	if h.Private, err = read(key); err != nil {
		return nil, err
	}
	if h.Public, err = read(key + ".pub"); err != nil {
		return nil, err
	}
	if h.Certificate, err = read(key + "-cert.pub"); err != nil {
		return nil, err
	}
	f := strings.Fields(h.Public)
	if len(f) < 2 {
		return nil, fmt.Errorf("unexpected host public key")
	}
	h.KnownHostsLine = dnsHost + " " + f[0] + " " + f[1]
	return h, nil
}
