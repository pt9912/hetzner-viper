// Package natsserver is what the NATS server of a group is made from: the pinned release of
// nats-server, its configuration and its systemd unit. It only builds text; installing it on the
// server is the use case's (internal/usecase/start).
package natsserver

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/nats-io/nkeys"
	"github.com/pt9912/hetzner-viper/internal/model"
)

// Version is the release of nats-server viper installs.
const Version = "v2.15.2"

// checksums are the SHA-256 sums of the release tarballs, from the release's SHA256SUMS file.
// They are pinned here so the download is checked against what was reviewed, not against what the
// download server says today.
func checksums() map[string]string {
	return map[string]string{
		"amd64": "67cb8e10bc6f0c4b47f75a728529a20953dbae2a4f9a4a52ae8517d65ae303ff",
		"arm64": "bc2c788dcbf2849af749564e06a0d48c19bced851f2d68a2e21c54d078949b28",
	}
}

// CLIVersion is the release of the nats command line tool viper installs on the machines of a
// group (to check the connection; people use it too).
const CLIVersion = "v0.5.1"

func cliChecksums() map[string]string {
	return map[string]string{
		"amd64": "f8a3e106deebed7afe736b7090359bda3aa62a04b310440bedc1d11a93c0ab4f",
		"arm64": "91c661f1173ee9bfe07514eef55a29d25ac7757a0868c179e825eba976073adc",
	}
}

// CLIReleaseFor is the nats tool for a machine; arch is what `uname -m` says.
func CLIReleaseFor(arch string) (Release, error) {
	r, err := ReleaseFor(arch)
	if err != nil {
		return Release{}, err
	}
	v := strings.TrimPrefix(CLIVersion, "v")
	return Release{
		Version: CLIVersion, Arch: r.Arch, SHA256: cliChecksums()[r.Arch],
		URL: fmt.Sprintf("https://github.com/nats-io/natscli/releases/download/%s/nats-%s-linux-%s.zip", CLIVersion, v, r.Arch),
	}, nil
}

// Release is the tarball to install on a machine of an architecture.
type Release struct {
	Version, Arch, URL, SHA256 string
}

// ReleaseFor is the release for a machine; arch is what `uname -m` says ("x86_64", "aarch64").
func ReleaseFor(arch string) (Release, error) {
	var goarch string
	switch arch {
	case "x86_64", "amd64":
		goarch = "amd64"
	case "aarch64", "arm64":
		goarch = "arm64"
	default:
		return Release{}, fmt.Errorf("nats-server: no release for the architecture %q", arch)
	}
	return Release{
		Version: Version, Arch: goarch, SHA256: checksums()[goarch],
		URL: fmt.Sprintf("https://github.com/nats-io/nats-server/releases/download/%s/nats-server-%s-linux-%s.tar.gz", Version, Version, goarch),
	}, nil
}

// Paths on the server. The directory belongs to the login user and is closed to others, so the
// key never needs more than that.
const (
	Dir        = "/home/builder/.viper-nats"
	ConfigFile = Dir + "/server.conf"
	CertFile   = Dir + "/server.crt"
	KeyFile    = Dir + "/server.key"
	Binary     = "/usr/local/bin/nats-server"
	// MCPBinary is viper-nats-mcp on a machine (the MCP server of Claude Code and the subcommands viper runs over ssh).
	MCPBinary = Dir + "/viper-nats-mcp"
	Unit      = "viper-nats.service"
)

// Client is a machine that may use the server: its public NKey.
type Client struct {
	Machine   string
	PublicKey string // starts with "U"
}

// Config is the configuration of the server.
type Config struct {
	Group string
	// Host is the private IP the server listens on, never "0.0.0.0".
	Host    string
	Port    int
	Clients []Client
}

// Permissions are the subjects a machine may use (the default policy, §6 of the plan): it
// publishes to its own subtree and to the request subjects of the others, and listens to the
// group and to its own inbox. Nothing else, so no machine can reach another group.
//
// The users get no allow_responses: it lets a client publish to the reply subject of any message
// delivered to it, without looking at the publish list. Every machine reads viper.<g>.> and the
// reply subject of a message is chosen by its sender, so that would let any machine publish to any
// subject (checked with nats-server 2.15.2: a machine can send its own message with a foreign
// reply subject, receive it back and answer it). Answers are therefore plain messages in the
// answering machine's own subtree.
func Permissions(group, machine string) (publish, subscribe []string) {
	publish = []string{
		fmt.Sprintf("viper.%s.%s.>", group, machine),
		fmt.Sprintf("viper.%s.*.req.>", group),
	}
	subscribe = []string{
		fmt.Sprintf("viper.%s.>", group),
		fmt.Sprintf("_INBOX.%s.>", machine),
	}
	return publish, subscribe
}

func quoteList(l []string) string {
	q := make([]string, len(l))
	for i, s := range l {
		q[i] = `"` + s + `"`
	}
	return strings.Join(q, ", ")
}

// checkedClients validates the group and the machines and returns the machines sorted by name.
func checkedClients(group string, in []Client) ([]Client, error) {
	if err := model.CheckNATSName("NATS group", group); err != nil {
		return nil, err
	}
	clients := append([]Client(nil), in...)
	sort.Slice(clients, func(i, j int) bool { return clients[i].Machine < clients[j].Machine })
	seen := map[string]bool{}
	for _, cl := range clients {
		if err := model.CheckNATSName("Machine name", cl.Machine); err != nil {
			return nil, err
		}
		if seen[cl.Machine] {
			return nil, fmt.Errorf("the machine %s is registered twice", cl.Machine)
		}
		seen[cl.Machine] = true
		if !validUserKey(cl.PublicKey) {
			return nil, fmt.Errorf("the key of %s is not a public user NKey", cl.Machine)
		}
	}
	if len(clients) == 0 {
		return nil, errors.New("nats-server needs at least one registered machine (no anonymous access)")
	}
	return clients, nil
}

// authorization is the authorization block of the configuration: the users with their keys and
// the permissions of Permissions.
func authorization(group string, clients []Client) string {
	var b strings.Builder
	b.WriteString("authorization {\n  timeout: 5\n  users = [\n")
	for _, cl := range clients {
		pub, sub := Permissions(group, cl.Machine)
		fmt.Fprintf(&b, "    {\n      # %s\n      nkey: %s\n      permissions: {\n        publish: { allow: [%s] }\n        subscribe: { allow: [%s] }\n      }\n    }\n",
			cl.Machine, cl.PublicKey, quoteList(pub), quoteList(sub))
	}
	b.WriteString("  ]\n}\n")
	return b.String()
}

// AuthorizationBlock is the part of server.conf that holds the users and their permissions, with
// the same checks as Render. Tests put it behind a listener of their own (loopback, no TLS), so
// they run with the permissions of production.
func AuthorizationBlock(group string, clients []Client) (string, error) {
	cs, err := checkedClients(group, clients)
	if err != nil {
		return "", err
	}
	return authorization(group, cs), nil
}

// Render is the content of server.conf. It refuses anything that could widen the server: a
// listen address that is not a private IPv4 address, names outside the alphabet of the plan, a
// key that is not a user NKey.
func (c Config) Render() (string, error) {
	clients, err := checkedClients(c.Group, c.Clients)
	if err != nil {
		return "", err
	}
	ip := net.ParseIP(c.Host)
	if ip == nil || ip.To4() == nil || !ip.IsPrivate() {
		return "", fmt.Errorf("nats-server listens on a private IPv4 address, not %q", c.Host)
	}
	if c.Port < 1024 || c.Port > 65535 {
		return "", fmt.Errorf("nats-server port %d", c.Port)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# written by viper; changes are overwritten\n")
	for _, cl := range clients {
		fmt.Fprintf(&b, "%s%s %s\n", registryPrefix, cl.Machine, cl.PublicKey) // read back by ParseClients
	}
	fmt.Fprintf(&b, "server_name: \"viper-%s\"\nhost: \"%s\"\nport: %d\n\n", c.Group, c.Host, c.Port)
	fmt.Fprintf(&b, "tls {\n  cert_file: \"%s\"\n  key_file: \"%s\"\n  min_version: \"1.2\"\n  timeout: 5\n}\n\n", CertFile, KeyFile)
	b.WriteString(authorization(c.Group, clients))
	return b.String(), nil
}

// validUserKey tells whether k looks like a public user NKey: 56 characters of base32, starting with "U".
func validUserKey(k string) bool {
	if len(k) != 56 || k[0] != 'U' {
		return false
	}
	for _, r := range k {
		upper, digit := r >= 'A' && r <= 'Z', r >= '2' && r <= '7'
		if !upper && !digit {
			return false
		}
	}
	return true
}

// UnitFile is the systemd unit of the server. It starts after the network is online and starts
// again if it cannot bind yet, because the private interface may come up a moment after boot.
func UnitFile() string {
	return `[Unit]
Description=NATS server of a viper group
After=network-online.target
Wants=network-online.target

[Service]
User=builder
ExecStart=` + Binary + ` -c ` + ConfigFile + `
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=3
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=read-only
ReadOnlyPaths=` + Dir + `
CapabilityBoundingSet=
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
`
}

// Identity is the NATS identity of a machine: a user NKey pair.
type Identity struct {
	Seed      string // private; starts with "SU", never leaves the machine it is written to
	PublicKey string // starts with "U"
}

// NewIdentity creates a new identity in memory.
func NewIdentity() (Identity, error) {
	kp, err := nkeys.CreateUser()
	if err != nil {
		return Identity{}, err
	}
	defer kp.Wipe()
	seed, err := kp.Seed()
	if err != nil {
		return Identity{}, err
	}
	pub, err := kp.PublicKey()
	if err != nil {
		return Identity{}, err
	}
	return Identity{Seed: string(seed), PublicKey: pub}, nil
}

const registryPrefix = "# viper-client "

// ParseClients reads the machines out of a configuration Render wrote: the lines at its top that
// say which machine has which key. Lines it does not know are ignored.
func ParseClients(conf string) []Client {
	var out []Client
	for _, l := range strings.Split(conf, "\n") {
		rest, ok := strings.CutPrefix(l, registryPrefix)
		if !ok {
			continue
		}
		if f := strings.Fields(rest); len(f) == 2 {
			out = append(out, Client{Machine: f[0], PublicKey: f[1]})
		}
	}
	return out
}

// WithClient returns the clients with this one added, or its key replaced if the machine is there.
func WithClient(clients []Client, c Client) []Client {
	out := make([]Client, 0, len(clients)+1)
	for _, o := range clients {
		if o.Machine != c.Machine {
			out = append(out, o)
		}
	}
	return append(out, c)
}

// WithoutClient returns the clients without the machine.
func WithoutClient(clients []Client, machine string) []Client {
	out := make([]Client, 0, len(clients))
	for _, o := range clients {
		if o.Machine != machine {
			out = append(out, o)
		}
	}
	return out
}

// ClientEnv is the file connection.env a machine of the group finds in Dir: where the server is
// and which files to use, in the names the nats tool and most client libraries know.
func ClientEnv(group, machine, host string, port int) string {
	return fmt.Sprintf(`# written by viper
NATS_URL=tls://%s:%d
NATS_CA_FILE=%s
NATS_NKEY_SEED_FILE=%s/client.nk
NATS_INBOX_PREFIX=_INBOX.%s
VIPER_NATS_SUBJECT_PREFIX=viper.%s.%s
`, host, port, CertFile, Dir, machine, group, machine)
}

// Endpoint is where the server listens.
type Endpoint struct {
	Host string
	Port int
}

// ParseEndpoint reads host and port out of a configuration Render wrote.
func ParseEndpoint(conf string) (Endpoint, bool) {
	var e Endpoint
	for _, l := range strings.Split(conf, "\n") {
		if v, ok := strings.CutPrefix(l, "host: "); ok {
			e.Host = strings.Trim(strings.TrimSpace(v), `"`)
		}
		if v, ok := strings.CutPrefix(l, "port: "); ok {
			if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &e.Port); err != nil {
				return Endpoint{}, false
			}
		}
	}
	return e, e.Host != "" && e.Port != 0
}
