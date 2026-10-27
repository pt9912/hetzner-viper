// Package azure is the adapter of the cloud port (internal/port/driven/cloud) for Microsoft Azure:
// a server is a VM in a resource group, with a public IP address and a network interface of its
// own; the firewall is a network security group on that interface. Azure keeps no key objects: the
// login key goes with the VM. What viper creates is tagged viper=<server name>.
package azure

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// API is what the adapter needs of Azure, in its own terms. The adapter's logic runs against it;
// sdk.go makes it out of the Azure SDK.
type API interface {
	// VM returns the VM of that name with its public address, nil if there is none.
	VM(ctx context.Context, name string) (*VM, error)
	// CreateVM makes the public address, the network interface and the VM, and waits until they exist.
	CreateVM(ctx context.Context, l Launch) error
	// DeleteVM deletes the VM, its network interface and its public address, and waits.
	DeleteVM(ctx context.Context, name string) error
	// Subnet returns the ID of the subnet the VMs go to, "" if it does not exist yet.
	Subnet(ctx context.Context) (string, error)
	// EnsureNetwork makes the resource group, the virtual network and the subnet (or only the
	// group, if a subnet is given) and returns the subnet ID.
	EnsureNetwork(ctx context.Context) (string, error)
	// NSG returns what the network security group of that name allows, nil if there is none.
	NSG(ctx context.Context, name string) (*NSG, error)
	// CreateNSG makes the group with the rules for SSH and ping from the networks.
	CreateNSG(ctx context.Context, name string, sources []string) error
	// AddNSGRules adds the rule for SSH and/or ping to an existing group.
	AddNSGRules(ctx context.Context, name string, ssh, icmp bool, sources []string) error
	// VMSize returns the VM size in the location, nil if there is none.
	VMSize(ctx context.Context, name string) (*Size, error)
	// ImageExists reports whether the marketplace image exists in the location.
	ImageExists(ctx context.Context, img Image) (bool, error)
}

// VM is a VM as the adapter needs it.
type VM struct {
	Name, IP string
	Created  time.Time
}

// Image is a marketplace image.
type Image struct{ Publisher, Offer, SKU, Version string }

func (i Image) String() string { return i.Publisher + ":" + i.Offer + ":" + i.SKU + ":" + i.Version }

// Launch is what a new VM is made from.
type Launch struct {
	Name, Size, SubnetID, NSG, UserData, SSHKey string
	Image                                       Image
	DiskGB                                      int32
}

// NSG says what a network security group lets in.
type NSG struct{ SSH, ICMP bool }

// Size is what the preflight shows of a VM size.
type Size struct{ VCPUs, MemoryMB int32 }

// Options are the settings of the adapter.
type Options struct {
	SubscriptionID string // AZURE_SUBSCRIPTION_ID
	ResourceGroup  string // AZURE_RESOURCE_GROUP, "" for viper
	Location       string // SERVER_LOCATION, e.g. germanywestcentral
	SubnetID       string // AZURE_SUBNET_ID, "" for a network of viper's own
	RootGB         int32  // size of the OS disk
	// Poll is how long to wait between two looks at a VM (default 3 s).
	Poll time.Duration
}

func (o Options) group() string {
	if o.ResourceGroup != "" {
		return o.ResourceGroup
	}
	return "viper"
}

func (o Options) diskGB() int32 {
	if o.RootGB > 0 {
		return o.RootGB
	}
	return 64
}

func (o Options) poll() time.Duration {
	if o.Poll > 0 {
		return o.Poll
	}
	return 3 * time.Second
}

// Client talks to Azure with the default Azure credentials (az login, environment variables, a
// managed identity).
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
func (*Client) Name() string { return "Azure" }

// Title is the name of the provider in the preflight.
func (*Client) Title() string { return "Microsoft Azure" }

// Hint says what to check when the API cannot be used.
func (*Client) Hint() string {
	return "Azure credentials (az login) / AZURE_SUBSCRIPTION_ID / network"
}

// Describe is an API error without the request details, safe to show.
func (*Client) Describe(err error) string {
	if err == nil {
		return ""
	}
	var re *azcore.ResponseError
	if errors.As(err, &re) {
		return fmt.Sprintf("%d %s", re.StatusCode, re.ErrorCode)
	}
	if strings.Contains(err.Error(), "DefaultAzureCredential") || strings.Contains(err.Error(), "failed to acquire a token") {
		return "no Azure credentials (run az login)"
	}
	return strings.TrimSpace(err.Error())
}

// client makes the API when it is first needed.
// session is the API of a client once it is made.
type session struct{ API }

func (c *Client) client(ctx context.Context) (*session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.api != nil {
		return &session{c.api}, nil
	}
	if c.opts.SubscriptionID == "" {
		return nil, errors.New("AZURE_SUBSCRIPTION_ID is not set")
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
