package model

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// NATSServiceHost is the name the clients of a NATS group in a Kubernetes cluster connect to: the headless Service
// of the group in its namespace. The address of the pod of the server changes with every new pod, the name does not.
func NATSServiceHost(group, namespace string) string {
	return "viper-nats-" + group + "." + namespace + ".svc"
}

// OperatorKeyFile is where the seed of the operator of a NATS group is kept on this computer:
// <state>/groups/<group>/operator.nk. A key that an older version put in <state>/nats/<group>/ is moved there
// (best effort) when it is asked for.
func OperatorKeyFile(stateDir, group string) string {
	path := filepath.Join(stateDir, "groups", group, "operator.nk")
	old := filepath.Join(stateDir, "nats", group, "operator.nk")
	if _, err := os.Stat(path); err != nil {
		if _, err := os.Stat(old); err == nil && os.MkdirAll(filepath.Dir(path), 0o700) == nil {
			_ = os.Rename(old, path)
		}
	}
	return path
}

// NATSRole is what a machine does in its NATS group.
type NATSRole string

// The roles of a machine in a NATS group.
const (
	NATSServer NATSRole = "server"
	NATSClient NATSRole = "client"
)

// NATSState is how far a machine is in setting up its NATS role.
type NATSState string

// The states of a machine's NATS role.
const (
	NATSProvisioning NATSState = "provisioning"
	NATSReady        NATSState = "ready"
	NATSFailed       NATSState = "failed"
)

// DefaultNATSOperator is the name of the operator in a NATS group unless NATS_OPERATOR_NAME says otherwise.
const DefaultNATSOperator = "operator"

// DefaultNATSPort is the port of the NATS server when NATS_PORT is not set.
const DefaultNATSPort = 4222

// natsNameRe is what a NATS group or machine name must match. The names are part of NATS subjects
// (no ".", "*", ">" or blanks, which would let a name reach into other subjects), of provider
// labels and of resource names, so the alphabet is the narrowest of the three.
var natsNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,29}[a-z0-9])?$`)

// CheckNATSName tells whether name is a usable NATS group or machine name; what names the error
// is, e.g. "NATS_GROUP". It does not rewrite the name.
func CheckNATSName(what, name string) error {
	if !natsNameRe.MatchString(name) {
		return fmt.Errorf("%s: 1 to 31 characters, lowercase letters, digits and hyphens, starting and ending with a letter or digit: %q", what, name)
	}
	return nil
}

// MaxMCPTopics is how many topics an MCP server holds (the default ones included).
const MaxMCPTopics = 16

// NATSConfig is the NATS group a machine belongs to, as the settings describe it. The zero value
// means NATS is off: the machine behaves as it did before.
type NATSConfig struct {
	Enabled bool
	Group   string
	Port    int
	// Operator is the name the person who runs viper sends under (NATS_OPERATOR_NAME, default "operator";
	// viper nats init). No machine of the group may have it.
	Operator string

	// MCP: install the NATS MCP server (viper-nats-mcp) on the machine and register it with Claude
	// Code (NATS_MCP). It needs Enabled.
	MCP bool
	// MCPPushFrom are the machines whose messages the MCP server pushes into the session of the agent
	// (NATS_MCP_PUSH_FROM); empty: none.
	MCPPushFrom []string
	// MCPTopics are the topics the MCP server subscribes to when it starts, on every machine
	// (NATS_MCP_TOPICS); they cannot be unsubscribed at run time. At most MaxMCPTopics.
	MCPTopics []string
	// MCPAutoAllow is whether viper allows the tools of the MCP server in the settings of Claude Code
	// without asking each time: "off" (default), "read" (peers, inbox, topics) or "all".
	MCPAutoAllow string
	// MCPBinary is a file to install instead of the one built into viper (NATS_MCP_BINARY, for
	// builds that have none).
	MCPBinary string

	// Objects: the NATS server of the group has an object store for images, PDFs and ZIP files (NATS_OBJECTS;
	// docs/plan/nats-objekte.md). The sizes are in bytes, the time to live in days. They are fixed when the server is set up.
	Objects        bool
	ObjectsMax     int64 // NATS_OBJECTS_MAX: the whole store
	ObjectsFileMax int64 // NATS_OBJECTS_FILE_MAX: one file
	ObjectsTTLDays int   // NATS_OBJECTS_TTL
}

// The defaults and bounds of the settings of the object store (sizes in MiB).
const (
	DefaultObjectsMaxMiB     = 256
	DefaultObjectsFileMaxMiB = 8
	DefaultObjectsTTLDays    = 7
	MaxObjectsMaxMiB         = 100 << 10
	MinObjectsMaxMiB         = 16
	MaxObjectsFileMaxMiB     = 64
	MaxObjectsTTLDays        = 365
)

// ParseObjectsLimits reads NATS_OBJECTS_MAX and NATS_OBJECTS_FILE_MAX (whole MiB) and NATS_OBJECTS_TTL (whole days); empty
// is the default. A file may not be larger than the store.
func ParseObjectsLimits(maxMiB, fileMiB, ttlDays string) (maxBytes, fileBytes int64, ttl int, err error) {
	num := func(name, v string, def, lo, hi int) (int, error) {
		v = strings.TrimSpace(v)
		if v == "" {
			return def, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < lo || n > hi {
			return 0, fmt.Errorf("%s: a whole number from %d to %d: %s", name, lo, hi, v)
		}
		return n, nil
	}
	m, err := num("NATS_OBJECTS_MAX", maxMiB, DefaultObjectsMaxMiB, MinObjectsMaxMiB, MaxObjectsMaxMiB)
	if err != nil {
		return 0, 0, 0, err
	}
	f, err := num("NATS_OBJECTS_FILE_MAX", fileMiB, DefaultObjectsFileMaxMiB, 1, MaxObjectsFileMaxMiB)
	if err != nil {
		return 0, 0, 0, err
	}
	if f > m {
		return 0, 0, 0, fmt.Errorf("NATS_OBJECTS_FILE_MAX: %d MiB is more than NATS_OBJECTS_MAX (%d MiB)", f, m)
	}
	if ttl, err = num("NATS_OBJECTS_TTL", ttlDays, DefaultObjectsTTLDays, 1, MaxObjectsTTLDays); err != nil {
		return 0, 0, 0, err
	}
	return int64(m) << 20, int64(f) << 20, ttl, nil
}

// The values of NATS_MCP_AUTOALLOW.
const (
	AutoAllowOff  = "off"
	AutoAllowRead = "read"
	AutoAllowAll  = "all"
)

// MCPTools are the tools of the NATS MCP server, in the order they are listed.
func MCPTools() []string {
	return []string{"peers", "send", "broadcast", "request", "reply", "inbox", "subscribe", "unsubscribe", "topics"}
}

// MCPAllowedTools are the tools that NATS_MCP_AUTOALLOW lets the agent use without asking.
func MCPAllowedTools(mode string) []string {
	switch mode {
	case AutoAllowRead:
		return []string{"peers", "inbox", "topics"}
	case AutoAllowAll:
		return MCPTools()
	}
	return nil
}

// NATSMetadata is what is kept with a machine at the cloud provider (as labels, tags, ...).
type NATSMetadata struct {
	Group string
	Role  NATSRole
	State NATSState
}

// ParseNATSBool reads NATS_ENABLED: "1" or "true" is on; "", "0" or "false" is off.
func ParseNATSBool(name, v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true":
		return true, nil
	case "", "0", "false":
		return false, nil
	}
	return false, fmt.Errorf("%s: true or false: %s", name, v)
}

// ParseNATSPort reads NATS_PORT: "" is the default, else a port from 1024 to 65535.
func ParseNATSPort(v string) (int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return DefaultNATSPort, nil
	}
	p, err := strconv.Atoi(v)
	if err != nil || p < 1024 || p > 65535 {
		return 0, fmt.Errorf("NATS_PORT: a port from 1024 to 65535: %s", v)
	}
	return p, nil
}

// The labels viper keeps on a server of a NATS group (Hetzner: labels; other providers: tags).
const (
	LabelManaged   = "viper-managed"
	LabelNATSGroup = "viper-nats-group"
	LabelNATSRole  = "viper-nats-role"
	LabelNATSState = "viper-nats-state"
)

// NATSLabels are the labels a server of a NATS group carries.
func NATSLabels(m NATSMetadata) map[string]string {
	return map[string]string{
		LabelManaged:   "true",
		LabelNATSGroup: m.Group,
		LabelNATSRole:  string(m.Role),
		LabelNATSState: string(m.State),
	}
}

// NATSMetadataFromLabels reads the NATS labels of a server. ok is false if the server carries none
// of them (not a member of a NATS group); an error if it carries them in a form that makes no sense,
// so a half-labelled server is never taken for a member.
func NATSMetadataFromLabels(labels map[string]string) (m NATSMetadata, ok bool, err error) {
	group, hasGroup := labels[LabelNATSGroup]
	role, hasRole := labels[LabelNATSRole]
	state, hasState := labels[LabelNATSState]
	if !hasGroup && !hasRole && !hasState {
		return NATSMetadata{}, false, nil
	}
	if !hasGroup || !hasRole || !hasState {
		return NATSMetadata{}, false, fmt.Errorf("the labels %s, %s and %s must all be there or all be missing", LabelNATSGroup, LabelNATSRole, LabelNATSState)
	}
	m = NATSMetadata{Group: group, Role: NATSRole(role), State: NATSState(state)}
	if err := CheckNATSName(LabelNATSGroup, m.Group); err != nil {
		return NATSMetadata{}, false, err
	}
	switch m.Role {
	case NATSServer, NATSClient:
	default:
		return NATSMetadata{}, false, fmt.Errorf("%s: server or client: %s", LabelNATSRole, role)
	}
	switch m.State {
	case NATSProvisioning, NATSReady, NATSFailed:
	default:
		return NATSMetadata{}, false, fmt.Errorf("%s: provisioning, ready or failed: %s", LabelNATSState, state)
	}
	return m, true, nil
}

// GroupMember is a server of a NATS group together with what the group knows of it.
type GroupMember struct {
	Server Server
	NATS   NATSMetadata
}

// PrivateNetwork is the private network of a NATS group at the provider.
type PrivateNetwork struct {
	// ID identifies the network to the provider; opaque.
	ID   string
	Name string
	// IPRange is the address range of the network, e.g. "10.123.0.0/16".
	IPRange string
	// Created is when the network was made.
	Created time.Time
	// Zone is the network zone at the provider ("eu-central"); servers can only be attached from
	// locations in it.
	Zone string
	// Problems are the ways the rule that makes the network (security group, firewall rule) differs from
	// what viper makes; empty if it is as made. Only the providers whose network is such a rule fill it.
	Problems []string
}

// Group is a group of groups/<name>.env: the machines it names, in the order of the file; the first
// machine of a new NATS group becomes its server (docs/plan/gruppen.md).
type Group struct {
	Name     string
	File     string
	Machines []string
}

// NATSLine is one line of the output of the subcommands of viper-nats-mcp (watch, send, broadcast,
// request): one JSON object per event.
type NATSLine struct {
	Event string `json:"event"`           // ready, message, sent, answer
	Me    string `json:"me,omitempty"`    // ready: this machine
	Group string `json:"group,omitempty"` // ready
	From  string `json:"from,omitempty"`  // message, answer: the sender, as the subject proves it
	To    string `json:"to,omitempty"`    // message via out: the machine it is for
	Via   string `json:"via,omitempty"`   // message: out, pub or presence
	Topic string `json:"topic,omitempty"` // message via pub
	Type  string `json:"type,omitempty"`  // message: the kind of the envelope (msg, req, res, presence)
	ID    string `json:"id,omitempty"`
	Re    string `json:"re,omitempty"`
	State string `json:"state,omitempty"` // presence
	Query bool   `json:"query,omitempty"` // presence
	Text  string `json:"text,omitempty"`  // always cleaned
}

// Detail is the address range and zone of the network for a message; a provider that has no range of
// its own for the group leaves it out.
func (n PrivateNetwork) Detail() string {
	if n.IPRange == "" {
		return "zone " + n.Zone
	}
	return n.IPRange + ", zone " + n.Zone
}
