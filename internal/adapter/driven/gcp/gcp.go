// Package gcp is the adapter of the cloud port (internal/port/driven/cloud) for Google Compute
// Engine: a server is a VM in one zone of a project, the firewall is a pair of VPC firewall rules
// (IPv4 and IPv6 networks cannot share one) that apply to VMs with the network tag of the firewall
// name. GCE keeps no key objects: the login key goes into the VM's cloud-init like the other keys.
package gcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/api/googleapi"
)

// API is what the adapter needs of Compute Engine, in its own terms. The adapter's logic runs
// against it; sdk.go makes it out of the compute/v1 client.
type API interface {
	Instances
	Firewalls
	Lookups
}

// Instances is the part of the API for VMs, with their labels and network tags (the labels and tags
// serve NATS groups).
type Instances interface {
	// Instance returns the VM of that name, nil if there is none.
	Instance(ctx context.Context, name string) (*Instance, error)
	// CreateInstance starts a VM and waits until the operation is done.
	CreateInstance(ctx context.Context, l Launch) error
	// DeleteInstance deletes a VM and waits until the operation is done.
	DeleteInstance(ctx context.Context, name string) error
	// ListInstances returns the VMs of the zone that carry all these labels.
	ListInstances(ctx context.Context, labels map[string]string) ([]*Instance, error)
	// SetLabels puts the labels on the VM and keeps the ones it has (setLabels replaces them all and
	// wants the fingerprint: the call reads, merges and tries again when somebody was faster).
	SetLabels(ctx context.Context, name string, labels map[string]string) error
	// AddTag adds a network tag to the VM and keeps the others (setTags replaces them all, among them the
	// tag of the SSH firewall).
	AddTag(ctx context.Context, name, tag string) error
}

// Firewalls is the part of the API for the firewall rules: the SSH rules and the rule of a NATS group.
type Firewalls interface {
	// Firewall returns the rule of that name, nil if there is none.
	Firewall(ctx context.Context, name string) (*Rule, error)
	// CreateFirewall creates an ingress rule for SSH and ICMP.
	CreateFirewall(ctx context.Context, r Rule) error
	// AddFirewallAllowed adds SSH and/or ICMP to a rule, keeping what else it allows.
	AddFirewallAllowed(ctx context.Context, name string, ssh, icmp bool) error
	// NATSFirewall returns the rule of that name, nil if there is none.
	NATSFirewall(ctx context.Context, name string) (*NATSRule, error)
	// CreateNATSFirewall creates the rule; ErrExists if the name is taken (names are unique in a project).
	CreateNATSFirewall(ctx context.Context, r NATSRule) error
	// DeleteFirewall deletes a rule; one that is gone is no error.
	DeleteFirewall(ctx context.Context, name string) error
}

// Lookups is the part of the API for what a VM is made from.
type Lookups interface {
	// MachineType returns the machine type in the zone, nil if there is none.
	MachineType(ctx context.Context, name string) (*MachineType, error)
	// ImageFamily returns the newest image of a family of a project, "" if there is none.
	ImageFamily(ctx context.Context, project, family string) (string, error)
	// NetworkExists reports whether the VPC network exists in the project.
	NetworkExists(ctx context.Context, name string) (bool, error)
}

// ErrExists is returned by CreateNATSFirewall for a name that is taken.
var ErrExists = errors.New("already exists")

// NATSRule is the ingress rule of a NATS group: the port, from and to the VMs with the tag.
type NATSRule struct {
	Name, Network, Tag, Description string
	Port                            int
}

// Instance is a VM as the adapter needs it.
type Instance struct {
	Name, Status, IP string
	Created          time.Time
	// PrivateIP is the address in the VPC; Labels and Tags are the labels and network tags of the VM.
	PrivateIP string
	Labels    map[string]string
	Tags      []string
}

// Launch is what a new VM is made from.
type Launch struct {
	Name, MachineType, Image, Network, Subnet, Tag, UserData string
	DiskGB                                                   int64
	// Labels are put on the VM besides viper=<name>; ExtraTags are network tags besides Tag.
	Labels    map[string]string
	ExtraTags []string
}

// Rule is an ingress firewall rule for SSH and ICMP.
type Rule struct {
	Name, Network, Tag string
	Ranges             []string
	SSH, ICMP          bool
}

// MachineType is what the preflight shows of a machine type.
type MachineType struct {
	VCPUs, MemoryMB int64
}

// Options are the settings of the adapter.
type Options struct {
	Project string // GCP_PROJECT
	Zone    string // SERVER_LOCATION, e.g. europe-west3-b
	Network string // GCP_NETWORK, "" for default
	Subnet  string // GCP_SUBNET: the subnet of a custom-mode network, "" for an auto-mode one
	RootGB  int64  // size of the boot disk
	// NATSPort is the port of the NATS server of a group (NATS_PORT); the rule of the group lets it in. 0: 4222.
	NATSPort int
	// Poll is how long to wait between two looks at a VM (default 3 s).
	Poll time.Duration
}

// Client talks to Compute Engine with the application default credentials (gcloud auth
// application-default login, a service account file, the metadata server).
type Client struct {
	opts Options

	mu  sync.Mutex
	api API
}

// New returns a client. It looks for the credentials when it is first used.
func New(opts Options) *Client { return &Client{opts: opts} }

// NewWith returns a client over a given API (for tests).
func NewWith(opts Options, api API) *Client { return &Client{opts: opts, api: api} }

// Name is the short name of the provider in messages.
func (*Client) Name() string { return "Google Cloud" }

// Title is the name of the provider in the preflight.
func (*Client) Title() string { return "Google Compute Engine" }

// Hint says what to check when the API cannot be used.
func (*Client) Hint() string {
	return "Google credentials (gcloud auth application-default login) / GCP_PROJECT / network"
}

// Describe is an API error without the request details, safe to show.
func (*Client) Describe(err error) string {
	if err == nil {
		return ""
	}
	var ge *googleapi.Error
	if errors.As(err, &ge) {
		return fmt.Sprintf("%d: %s", ge.Code, ge.Message)
	}
	if strings.Contains(err.Error(), "could not find default credentials") {
		return "no Google credentials (run gcloud auth application-default login)"
	}
	return strings.TrimSpace(err.Error())
}

func (o Options) network() string {
	if o.Network != "" {
		return o.Network
	}
	return "default"
}

func (o Options) diskGB() int64 {
	if o.RootGB > 0 {
		return o.RootGB
	}
	return 50
}

func (o Options) poll() time.Duration {
	if o.Poll > 0 {
		return o.Poll
	}
	return 3 * time.Second
}

// session is the API of a client once it is made. client returns it instead of the interface
// itself, which the linter (ireturn) does not allow; the methods of API are its own.
type session struct{ API }

// client makes the API when it is first needed.
func (c *Client) client(ctx context.Context) (*session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.api != nil {
		return &session{c.api}, nil
	}
	if c.opts.Project == "" {
		return nil, errors.New("GCP_PROJECT is not set")
	}
	api, err := newSDK(ctx, c.opts)
	if err != nil {
		return nil, err
	}
	c.api = api
	return &session{api}, nil
}

// wait calls done until it reports true, looking every Poll, for at most five minutes.
func (c *Client) wait(ctx context.Context, what string, done func() (bool, error)) error {
	deadline := time.Now().Add(5 * time.Minute)
	for {
		ok, err := done()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %s", what)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.opts.poll()):
		}
	}
}
