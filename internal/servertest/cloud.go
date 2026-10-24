package servertest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pt9912/hetzner-viper/internal/hetzner"
)

// Cloud is a Hetzner project and a Cloudflare zone with state: what the commands create is there
// for the next command. It implements the interfaces of preflight, start, check and stop.
type Cloud struct {
	mu sync.Mutex

	Now           time.Time
	Servers       map[string]*hetzner.Server
	nextID        int64
	Keys          []hetzner.SSHKey
	Firewalls     map[string][]hetzner.FirewallRule
	Types         map[string]*hetzner.ServerType
	Locations     map[string]bool
	Images        map[string]bool // "name/arch"
	UserData      map[string]string
	Created       []hetzner.ServerSpec
	Deleted       []string
	Calls         []string // what was changed, in order: "key create ...", "server create ...", ...
	ListErr       error    // every call of the project fails with it
	DNSRecords    map[string]DNSRecord
	DNSZone       string
	DNSFail       bool // setting a record fails
	DNSDeleteFail bool // deleting a record fails
	// FirewallErr makes reading a firewall fail (the preflight does not read firewalls).
	FirewallErr error
}

// DNSRecord is an A record; Ours is whether it carries the comment of this tool.
type DNSRecord struct {
	IP   string
	Ours bool
}

// NewCloud is an empty project with the usual server types, locations and image.
func NewCloud(now time.Time) *Cloud {
	loc := func(avail ...string) []hetzner.ServerTypeLocation {
		var out []hetzner.ServerTypeLocation
		for _, n := range avail {
			out = append(out, hetzner.ServerTypeLocation{Name: n, Available: true})
		}
		return out
	}
	return &Cloud{
		Now: now, Servers: map[string]*hetzner.Server{}, nextID: 1000, Firewalls: map[string][]hetzner.FirewallRule{},
		Types: map[string]*hetzner.ServerType{
			"cpx32": {Name: "cpx32", Cores: 4, MemoryGB: 8, DiskGB: 160, Architecture: "x86", Locations: loc("fsn1", "nbg1", "hel1")},
			"cpx02": {Name: "cpx02", Cores: 2, MemoryGB: 4, DiskGB: 80, Architecture: "x86", Locations: loc("fsn1", "nbg1")},
			"cax11": {Name: "cax11", Cores: 2, MemoryGB: 4, DiskGB: 40, Architecture: "arm", Locations: loc("fsn1")},
		},
		Locations: map[string]bool{"fsn1": true, "nbg1": true, "hel1": true},
		Images:    map[string]bool{"ubuntu-26.04/x86": true, "ubuntu-26.04/arm": true},
		UserData:  map[string]string{}, DNSRecords: map[string]DNSRecord{}, DNSZone: "example.com",
	}
}

func (c *Cloud) call(format string, a ...any) { c.Calls = append(c.Calls, fmt.Sprintf(format, a...)) }

// Changes are the calls that created or deleted something.
func (c *Cloud) Changes() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.Calls...)
}

// --- Hetzner

// ServerNames lists the names of the servers.
func (c *Cloud) ServerNames(context.Context) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ListErr != nil {
		return nil, c.ListErr
	}
	var names []string
	for n := range c.Servers {
		names = append(names, n)
	}
	return names, nil
}

// ServerByName returns a copy of the server, or nil.
func (c *Cloud) ServerByName(_ context.Context, name string) (*hetzner.Server, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ListErr != nil {
		return nil, c.ListErr
	}
	if s := c.Servers[name]; s != nil {
		cp := *s
		return &cp, nil
	}
	return nil, nil
}

// ServerType returns the server type, or nil if it does not exist.
func (c *Cloud) ServerType(_ context.Context, name string) (*hetzner.ServerType, error) {
	return c.Types[name], nil
}

// LocationExists reports whether the location exists.
func (c *Cloud) LocationExists(_ context.Context, name string) (bool, error) {
	return c.Locations[name], nil
}

// ImageExists reports whether the image exists for the architecture.
func (c *Cloud) ImageExists(_ context.Context, name, arch string) (bool, error) {
	return c.Images[name+"/"+arch], nil
}

// SSHKeys lists the public keys of the project.
func (c *Cloud) SSHKeys(context.Context) ([]hetzner.SSHKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ListErr != nil {
		return nil, c.ListErr
	}
	return append([]hetzner.SSHKey(nil), c.Keys...), nil
}

// SSHKeyCreate adds a public key to the project.
func (c *Cloud) SSHKeyCreate(_ context.Context, name, pub string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.call("ssh-key create %s", name)
	// the fingerprint is the one of the key file: tests that reuse a key compare it themselves
	c.Keys = append(c.Keys, hetzner.SSHKey{Name: name, Fingerprint: fingerprintOf(pub)})
	return nil
}

// FirewallRules returns the rules of a firewall and whether it exists.
func (c *Cloud) FirewallRules(_ context.Context, name string) ([]hetzner.FirewallRule, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ListErr != nil {
		return nil, false, c.ListErr
	}
	if c.FirewallErr != nil {
		return nil, false, c.FirewallErr
	}
	r, ok := c.Firewalls[name]
	return append([]hetzner.FirewallRule(nil), r...), ok, nil
}

// FirewallCreate creates an empty firewall.
func (c *Cloud) FirewallCreate(_ context.Context, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.call("firewall create %s", name)
	c.Firewalls[name] = nil
	return nil
}

// FirewallAddRule adds a rule to a firewall.
func (c *Cloud) FirewallAddRule(_ context.Context, name string, r hetzner.FirewallRule) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.call("firewall add-rule %s %s %s %s", name, r.Protocol, r.Port, strings.Join(r.SourceIPs, ","))
	c.Firewalls[name] = append(c.Firewalls[name], r)
	return nil
}

// ServerCreate creates a server.
func (c *Cloud) ServerCreate(_ context.Context, s hetzner.ServerSpec) (*hetzner.Server, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.call("server create %s --type %s --location %s --image %s --ssh-key %s --firewall %s", s.Name, s.Type, s.Location, s.Image, s.SSHKey, s.Firewall)
	c.nextID++
	srv := &hetzner.Server{ID: c.nextID, Name: s.Name, IPv4: fmt.Sprintf("203.0.113.%d", 10+len(c.Servers)), Created: c.Now}
	c.Servers[s.Name] = srv
	c.UserData[s.Name] = s.UserData
	c.Created = append(c.Created, s)
	cp := *srv
	return &cp, nil
}

// DeleteServer deletes a server by ID.
func (c *Cloud) DeleteServer(_ context.Context, id int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for n, s := range c.Servers {
		if s.ID == id {
			c.call("server delete %s", n)
			delete(c.Servers, n)
			c.Deleted = append(c.Deleted, n)
			return nil
		}
	}
	return errors.New("no such server")
}

// --- Cloudflare

// Set creates or updates the DNS record of a host.
func (c *Cloud) Set(_ context.Context, host, ip string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.DNSFail {
		return "", errors.New("Cloudflare API error: fake")
	}
	if r, ok := c.DNSRecords[host]; ok && !r.Ours {
		return "", errors.New("Existing DNS record is not managed by this setup; refusing overwrite")
	}
	c.call("dns set %s %s", host, ip)
	c.DNSRecords[host] = DNSRecord{IP: ip, Ours: true}
	return host + " -> " + ip, nil
}

// Delete removes the DNS record of a host if it still points to expectedIP.
func (c *Cloud) Delete(_ context.Context, host, expectedIP string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.DNSDeleteFail {
		return "", errors.New("Cloudflare API error: fake")
	}
	if r, ok := c.DNSRecords[host]; ok {
		if !r.Ours {
			return "", errors.New("DNS record not owned by setup; skipping")
		}
		if expectedIP != "" && r.IP != expectedIP {
			return "", errors.New("DNS IP changed; refusing deletion")
		}
		c.call("dns delete %s %s", host, r.IP)
		delete(c.DNSRecords, host)
	}
	return "DNS record removed (if present)", nil
}

// Check reports whether the zone is usable for the domain.
func (c *Cloud) Check(_ context.Context, domain, host string) ([]string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if domain != c.DNSZone && !strings.HasSuffix(domain, "."+c.DNSZone) {
		return []string{fmt.Sprintf("FAIL  DNS_DOMAIN %s is not in Cloudflare zone %s", domain, c.DNSZone)}, false, nil
	}
	lines := []string{fmt.Sprintf("ok    Cloudflare zone %s readable (edit permission is only seen when viper up writes the record)", c.DNSZone)}
	r, ok := c.DNSRecords[host]
	switch {
	case !ok:
		lines = append(lines, fmt.Sprintf("ok    %s: no record yet", host))
	case r.Ours:
		lines = append(lines, fmt.Sprintf("info  %s -> %s (managed by this setup)", host, r.IP))
	default:
		return append(lines, fmt.Sprintf("FAIL  %s already has a record not managed by this setup", host)), false, nil
	}
	return lines, true, nil
}

// fingerprintOf is the MD5 fingerprint of an OpenSSH public key line.
func fingerprintOf(pub string) string { return Fingerprint(pub) }
