// Package model is what viper talks about, in terms that belong to no provider and no tool: a
// server, the key and the firewall that let you in, and what a check found. It imports nothing
// of viper; the ports (internal/port) are written in it.
package model

import (
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

// Server is a server as far as the use cases need it.
type Server struct {
	// ID identifies the server to the provider; opaque (a number, an instance ID, a resource path).
	ID   string
	Name string
	IPv4 string
	// Created is when the server was created.
	Created time.Time
}

// ServerSpec is what a new server is made from. Size, Region and Image are the provider's own
// names (Hetzner: server type, location, image; AWS: instance type, region, AMI; ...).
type ServerSpec struct {
	Name, Size, Region, Image string
	SSHKey                    string // Ref of the login key
	Firewall                  string // name of the firewall
	UserData                  string // cloud-init
}

// LoginKey is the login key in the provider.
type LoginKey struct {
	// Ref is what ServerSpec.SSHKey takes: the name of the key in the provider, or the key itself
	// where the provider only takes keys with the server.
	Ref string
	// Created is whether the key was uploaded now (false: it was there already).
	Created bool
}

// FirewallResult says what making sure of the firewall did.
type FirewallResult struct {
	Created bool // the firewall did not exist
	// Complete is whether an existing firewall already had the rules: its other rules are not
	// known to be right for this connection.
	Complete bool
}

// Level is how serious a Finding is.
type Level int

// The levels of a Finding.
const (
	OK Level = iota
	Info
	Warn
	Fail
)

// Finding is one result of a check.
type Finding struct {
	Level Level
	Text  string
}

// Check is what a provider looks at to tell whether the server can be created.
type Check struct {
	Machine    string // name of the machine, for hints about settings files
	ServerName string
	Spec       ServerSpec
	KeyName    string // name the login key gets
	KeyFile    string // path of the login key (without .pub), for messages
	PublicKey  string // the login key's public key line, "" if it cannot be read
}

// FingerprintMD5 is the colon-separated MD5 fingerprint of an OpenSSH public key line, "" if it
// is none.
func FingerprintMD5(pub string) string {
	f := strings.Fields(pub)
	if len(f) < 2 {
		return ""
	}
	blob, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil {
		return ""
	}
	sum := md5.Sum(blob)
	parts := make([]string, len(sum))
	for i, x := range sum {
		parts[i] = fmt.Sprintf("%02x", x)
	}
	return strings.Join(parts, ":")
}
