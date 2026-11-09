package model

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

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

// NATSConfig is the NATS group a machine belongs to, as the settings describe it. The zero value
// means NATS is off: the machine behaves as it did before.
type NATSConfig struct {
	Enabled bool
	Group   string
	Port    int

	// MCP: install the NATS MCP server (viper-nats-mcp) on the machine and register it with Claude
	// Code (NATS_MCP). It needs Enabled.
	MCP bool
	// MCPPushFrom are the machines whose messages the MCP server pushes into the session of the agent
	// (NATS_MCP_PUSH_FROM); empty: none.
	MCPPushFrom []string
	// MCPAutoAllow is whether viper allows the tools of the MCP server in the settings of Claude Code
	// without asking each time: "off" (default), "read" (peers, inbox) or "all".
	MCPAutoAllow string
	// MCPBinary is a file to install instead of the one built into viper (NATS_MCP_BINARY, for
	// builds that have none).
	MCPBinary string
}

// The values of NATS_MCP_AUTOALLOW.
const (
	AutoAllowOff  = "off"
	AutoAllowRead = "read"
	AutoAllowAll  = "all"
)

// MCPTools are the tools of the NATS MCP server, in the order they are listed.
func MCPTools() []string {
	return []string{"peers", "send", "broadcast", "request", "reply", "inbox", "subscribe", "unsubscribe"}
}

// MCPAllowedTools are the tools that NATS_MCP_AUTOALLOW lets the agent use without asking.
func MCPAllowedTools(mode string) []string {
	switch mode {
	case AutoAllowRead:
		return []string{"peers", "inbox"}
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
}
