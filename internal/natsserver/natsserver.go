// Package natsserver is what the NATS server of a group is made from: the pinned release of
// nats-server, its configuration and its systemd unit. It only builds text; installing it on the
// server is the use case's (internal/usecase/start).
package natsserver

import (
	"errors"
	"fmt"
	"net"
	"regexp"
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
	Machine   string // the name; for an operator the name it sends under
	PublicKey string // starts with "U"
	// Operator marks the entry of the person who runs viper (viper nats init), not of a machine. It gets
	// the rights of OperatorPermissions and is kept when machines join or leave.
	Operator bool
	// Admin marks the entry viper makes the object store with (AdminName): it can create the bucket of the group and
	// nothing else (AdminPermissions). Its key stays on the machine of the server.
	Admin bool
}

// AdminName is the name of the entry of the group that makes the bucket of the object store. No machine may have it.
const AdminName = model.NATSAdminName

// ObjectBucket is the name of the bucket of the object store of a group; its stream is called OBJ_<bucket>.
func ObjectBucket(group string) string { return "viper-" + group }

// objectRights are the subjects a user of the object store may publish to: put and get objects of the bucket of the group
// (chunks and metadata, the lookup of a name, the consumer of a read), but not make, change, purge or delete the stream.
// Measured with nats-server 2.15.2 and nats.go 1.55.0 (docs/plan/nats-objekte.md §4).
func objectRights(group string) []string {
	b := ObjectBucket(group)
	return []string{
		fmt.Sprintf("$O.%s.C.*", b), fmt.Sprintf("$O.%s.M.*", b),
		fmt.Sprintf("$JS.API.STREAM.INFO.OBJ_%s", b),
		fmt.Sprintf("$JS.API.DIRECT.GET.OBJ_%s.>", b),
		fmt.Sprintf("$JS.API.CONSUMER.CREATE.OBJ_%s", b), fmt.Sprintf("$JS.API.CONSUMER.CREATE.OBJ_%s.>", b),
		fmt.Sprintf("$JS.API.CONSUMER.DELETE.OBJ_%s.*", b),
		// a read of a larger object (from about 1 MiB) uses flow control: the client answers the server on this subject, and a
		// read without the right hangs ("read pipe: i/o timeout"). Measured on k3d with a 3 MiB file.
		fmt.Sprintf("$JS.FC.OBJ_%s.>", b),
	}
}

// AdminPermissions are the rights of the entry that makes the bucket: create, change and look at the stream of the bucket
// of the group, and read the answers. Nothing in the group, no other bucket.
func AdminPermissions(group string) (publish, subscribe []string) {
	b := ObjectBucket(group)
	return []string{
			fmt.Sprintf("$JS.API.STREAM.CREATE.OBJ_%s", b), fmt.Sprintf("$JS.API.STREAM.UPDATE.OBJ_%s", b), fmt.Sprintf("$JS.API.STREAM.INFO.OBJ_%s", b),
		},
		[]string{"_INBOX." + AdminName + ".>"}
}

// Config is the configuration of the server.
type Config struct {
	Group string
	// Host is the private IP the server listens on, never "0.0.0.0". For a server in a pod (Listen is set) it is the
	// name the clients connect to: the address of the pod changes, the name of the Service does not.
	Host string
	// Listen is ListenPodIP for a server in a pod: it listens on the address of its pod, which nats-server reads from
	// the environment of the pod. Empty: it listens on Host.
	Listen  string
	Port    int
	Clients []Client
	// Objects, if set, turns JetStream on for the object store of the group (docs/plan/nats-objekte.md). Nil: JetStream
	// stays off, as it always was.
	Objects *Objects
}

// Objects is the object store of a group: where JetStream keeps its files and how much it may hold. It is written to
// server.conf as a line "# viper-objects ..." (read back by ParseEndpoint, so that a change of the list of the machines keeps
// it) and as the jetstream block.
type Objects struct {
	StoreDir string // ObjectStoreDir
	MaxBytes int64  // max_file_store: the whole store
	FileMax  int64  // the largest file
	TTLDays  int
}

// ObjectStoreDirVM is where the store is on a VM: outside Dir, which the unit makes read only. Pods and containers keep it
// in Dir (the home directory is a volume there).
const ObjectStoreDirVM = "/var/lib/viper-nats"

// ObjectStoreDir is the directory of the store on a machine: a container has the home directory on a volume, a VM uses
// the state directory of the unit.
func ObjectStoreDir(container bool) string {
	if container {
		return Dir + "/js"
	}
	return ObjectStoreDirVM
}

const objectsPrefix = "# viper-objects "

func (o Objects) validate() error {
	if o.StoreDir != ObjectStoreDirVM && o.StoreDir != Dir+"/js" {
		return fmt.Errorf("object store: the directory %q is not one viper uses", o.StoreDir)
	}
	if o.MaxBytes < 16<<20 || o.MaxBytes > 100<<30 {
		return fmt.Errorf("object store: the size %d is out of range", o.MaxBytes)
	}
	if o.FileMax < 1<<20 || o.FileMax > o.MaxBytes || o.FileMax > 64<<20 {
		return fmt.Errorf("object store: the file size %d is out of range", o.FileMax)
	}
	if o.TTLDays < 1 || o.TTLDays > 365 {
		return fmt.Errorf("object store: the time to live %d days is out of range", o.TTLDays)
	}
	return nil
}

// head is the line that keeps the settings of the store in server.conf ("" for none); the settings are checked first.
func (o *Objects) head() (string, error) {
	if o == nil {
		return "", nil
	}
	if err := o.validate(); err != nil {
		return "", err
	}
	return o.line() + "\n", nil
}

// block is the jetstream block of server.conf ("" for no object store: JetStream stays off).
func (o *Objects) block() string {
	if o == nil {
		return ""
	}
	return fmt.Sprintf("jetstream {\n  store_dir: \"%s\"\n  max_file_store: %d\n}\n\n", o.StoreDir, o.MaxBytes)
}

func (o Objects) line() string {
	return fmt.Sprintf("%sstore=%s max=%d file=%d ttl=%d", objectsPrefix, o.StoreDir, o.MaxBytes, o.FileMax, o.TTLDays)
}

// parseObjects reads the line Render wrote; nil if there is none or it does not pass the checks.
func parseObjects(conf string) *Objects {
	for _, l := range strings.Split(conf, "\n") {
		rest, ok := strings.CutPrefix(l, objectsPrefix)
		if !ok {
			continue
		}
		var o Objects
		if _, err := fmt.Sscanf(rest, "store=%s max=%d file=%d ttl=%d", &o.StoreDir, &o.MaxBytes, &o.FileMax, &o.TTLDays); err != nil || o.validate() != nil {
			return nil
		}
		return &o
	}
	return nil
}

// ListenPodIP is what a server in a pod puts as its listen address: nats-server takes the value of the variable
// POD_IP (the address of the pod, from the Downward API) when it reads the configuration.
const ListenPodIP = "$POD_IP"

// advertisePrefix is the line of server.conf that holds the name the clients connect to, for a server that listens on
// the address of its pod (read back by ParseEndpoint).
const advertisePrefix = "# viper-advertise "

// dnsNameRe is the name the clients of a server in a container connect to: the Service of a cluster with its namespace, or
// the alias of the server on the network of a Docker host (one label).
var dnsNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

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
func Permissions(group, machine string, objects bool) (publish, subscribe []string) {
	publish = []string{
		fmt.Sprintf("viper.%s.%s.>", group, machine),
		fmt.Sprintf("viper.%s.*.req.>", group),
	}
	subscribe = []string{
		fmt.Sprintf("viper.%s.>", group),
		fmt.Sprintf("_INBOX.%s.>", machine),
	}
	if objects { // the object store of the group (NATS_OBJECTS); the connection sets the inbox prefix _INBOX.<machine>
		publish = append(publish, objectRights(group)...)
	}
	return publish, subscribe
}

// OperatorPermissions are the subjects the operator may use: it publishes only to its own subtree and
// listens to the group. Unlike a machine it gets no right to the request subjects of the others (the one
// subtree where the subject does not prove the sender) and no inbox: the answer to a request is a plain
// message to viper.<g>.<answerer>.out.<operator>, which is inside the group subtree.
//
// With the object store (objects) it also gets the rights of a machine to put and get objects, and an inbox for the answers
// (_INBOX.<name>, which the connection sets as its inbox prefix): the one place where it may read what is sent to it.
func OperatorPermissions(group, name string, objects bool) (publish, subscribe []string) {
	publish, subscribe = []string{fmt.Sprintf("viper.%s.%s.>", group, name)}, []string{fmt.Sprintf("viper.%s.>", group)}
	if objects {
		publish = append(publish, objectRights(group)...)
		subscribe = append(subscribe, fmt.Sprintf("_INBOX.%s.>", name))
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
			return nil, fmt.Errorf("the name %s is registered twice (a machine and the operator cannot share a name)", cl.Machine)
		}
		seen[cl.Machine] = true
		if (cl.Machine == AdminName) != cl.Admin {
			return nil, fmt.Errorf("the name %s is only for the entry that makes the object store", AdminName)
		}
		if cl.Admin && cl.Operator {
			return nil, fmt.Errorf("%s cannot be an operator", cl.Machine)
		}
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
func authorization(group string, clients []Client, objects bool) string {
	var b strings.Builder
	b.WriteString("authorization {\n  timeout: 5\n  users = [\n")
	for _, cl := range clients {
		pub, sub := Permissions(group, cl.Machine, objects)
		switch {
		case cl.Operator:
			pub, sub = OperatorPermissions(group, cl.Machine, objects)
		case cl.Admin:
			pub, sub = AdminPermissions(group)
		}
		fmt.Fprintf(&b, "    {\n      # %s\n      nkey: %s\n      permissions: {\n        publish: { allow: [%s] }\n        subscribe: { allow: [%s] }\n      }\n    }\n",
			cl.Machine, cl.PublicKey, quoteList(pub), quoteList(sub))
	}
	b.WriteString("  ]\n}\n")
	return b.String()
}

// AuthorizationBlock is the part of server.conf that holds the users and their permissions, with
// the same checks as Render. Tests put it behind a listener of their own (loopback, no TLS), so
// they run with the permissions of production.
func AuthorizationBlock(group string, clients []Client, objects bool) (string, error) {
	cs, err := checkedClients(group, clients)
	if err != nil {
		return "", err
	}
	return authorization(group, cs, objects), nil
}

// listenAddress is what server.conf puts after "host:": the private address, or the address of the pod.
func (c Config) listenAddress() (string, error) {
	listen := fmt.Sprintf("%q", c.Host)
	if c.Listen != "" {
		if c.Listen != ListenPodIP || !dnsNameRe.MatchString(c.Host) || net.ParseIP(c.Host) != nil {
			return "", fmt.Errorf("nats-server in a pod listens on %s and is reached by a DNS name (a Service or a network alias), not %q (%q)", ListenPodIP, c.Listen, c.Host)
		}
		listen = ListenPodIP
	} else if ip := net.ParseIP(c.Host); ip == nil || ip.To4() == nil || !ip.IsPrivate() {
		return "", fmt.Errorf("nats-server listens on a private IPv4 address, not %q", c.Host)
	}
	return listen, nil
}

// Render is the content of server.conf. It refuses anything that could widen the server: a
// listen address that is not a private IPv4 address, names outside the alphabet of the plan, a
// key that is not a user NKey.
func (c Config) Render() (string, error) {
	clients, err := checkedClients(c.Group, c.Clients)
	if err != nil {
		return "", err
	}
	listen, err := c.listenAddress()
	if err != nil {
		return "", err
	}
	if c.Port < 1024 || c.Port > 65535 {
		return "", fmt.Errorf("nats-server port %d", c.Port)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# written by viper; changes are overwritten\n")
	if c.Listen != "" {
		fmt.Fprintf(&b, "%s%s\n", advertisePrefix, c.Host)
	}
	for _, cl := range clients {
		prefix := registryPrefix
		switch {
		case cl.Operator:
			prefix = operatorPrefix
		case cl.Admin:
			prefix = adminPrefix
		}
		fmt.Fprintf(&b, "%s%s %s\n", prefix, cl.Machine, cl.PublicKey) // read back by ParseClients
	}
	head, err := c.Objects.head()
	if err != nil {
		return "", err
	}
	b.WriteString(head)
	fmt.Fprintf(&b, "server_name: \"viper-%s\"\nhost: %s\nport: %d\n\n", c.Group, listen, c.Port)
	fmt.Fprintf(&b, "tls {\n  cert_file: \"%s\"\n  key_file: \"%s\"\n  min_version: \"1.2\"\n  timeout: 5\n}\n\n", CertFile, KeyFile)
	b.WriteString(c.Objects.block())
	b.WriteString(authorization(c.Group, clients, c.Objects != nil))
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
//
// With the object store (objects) the unit has a state directory of its own, /var/lib/viper-nats: the unit closes the
// directory of the server to writing, and the store must be written (docs/plan/nats-objekte.md §3).
func UnitFile(objects bool) string {
	state := ""
	if objects {
		state = "StateDirectory=viper-nats\n"
	}
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
` + state + `CapabilityBoundingSet=
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

// PublicKeyOf is the public key of a seed.
func PublicKeyOf(seed string) (string, error) {
	kp, err := nkeys.FromSeed([]byte(strings.TrimSpace(seed)))
	if err != nil {
		return "", fmt.Errorf("not an NKey seed: %w", err)
	}
	defer kp.Wipe()
	return kp.PublicKey()
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

const (
	registryPrefix = "# viper-client "
	operatorPrefix = "# viper-operator "
	adminPrefix    = "# viper-admin "
)

// ParseClients reads the machines out of a configuration Render wrote: the lines at its top that
// say which machine has which key. Lines it does not know are ignored.
func ParseClients(conf string) []Client {
	var out []Client
	for _, l := range strings.Split(conf, "\n") {
		var operator, admin bool
		rest, ok := strings.CutPrefix(l, registryPrefix)
		if !ok {
			if rest, ok = strings.CutPrefix(l, operatorPrefix); ok {
				operator = true
			} else if rest, ok = strings.CutPrefix(l, adminPrefix); ok {
				admin = true
			} else {
				continue
			}
		}
		if f := strings.Fields(rest); len(f) == 2 {
			out = append(out, Client{Machine: f[0], PublicKey: f[1], Operator: operator, Admin: admin})
		}
	}
	return out
}

// ErrNameTaken is returned by WithEntry for a name that belongs to an entry of the other kind.
var ErrNameTaken = errors.New("the name belongs to another entry (a machine and the operator cannot share a name)")

// WithEntry is WithClient that refuses to replace the entry of the other kind: a machine does not take
// the key of the operator and the operator not that of a machine.
func WithEntry(clients []Client, c Client) ([]Client, error) {
	for _, o := range clients {
		if o.Machine == c.Machine && (o.Operator != c.Operator || o.Admin != c.Admin) {
			return nil, fmt.Errorf("%s: %w", c.Machine, ErrNameTaken)
		}
	}
	return WithClient(clients, c), nil
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

// WithoutOperator returns the clients without the operator of that name (a machine of that name stays).
func WithoutOperator(clients []Client, name string) []Client {
	out := make([]Client, 0, len(clients))
	for _, o := range clients {
		if o.Machine != name || !o.Operator {
			out = append(out, o)
		}
	}
	return out
}

// WithoutClient returns the clients without the machine (an operator of that name stays).
func WithoutClient(clients []Client, machine string) []Client {
	out := make([]Client, 0, len(clients))
	for _, o := range clients {
		if o.Machine != machine || o.Operator {
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
	// Listen is ListenPodIP for a server in a pod (Host is then its Service name), else empty.
	Listen string
	Port   int
	// Objects is the object store of the group, if its server has one.
	Objects *Objects
}

// ParseEndpoint reads host and port out of a configuration Render wrote.
func ParseEndpoint(conf string) (Endpoint, bool) {
	var e Endpoint
	advertised := ""
	for _, l := range strings.Split(conf, "\n") {
		if v, ok := strings.CutPrefix(l, advertisePrefix); ok {
			advertised = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(l, "host: "); ok {
			e.Host = strings.Trim(strings.TrimSpace(v), `"`)
		}
		if v, ok := strings.CutPrefix(l, "port: "); ok {
			if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &e.Port); err != nil {
				return Endpoint{}, false
			}
		}
	}
	if e.Host == ListenPodIP { // a server in a pod: the name the clients use is the advertised one
		e.Listen, e.Host = ListenPodIP, advertised
	}
	e.Objects = parseObjects(conf)
	return e, e.Host != "" && e.Port != 0
}
