package cloud

import (
	"context"
	"errors"

	"github.com/pt9912/hetzner-viper/internal/model"
)

// NATSProvider is what a provider adds to be able to hold NATS groups. It is a capability apart
// from Provider, so the providers that cannot do it (yet) stay as they are; a caller asks for it
// with a type assertion and tells the user if it is missing.
type NATSProvider interface {
	// ListGroupMachines returns the servers that belong to the NATS group, with their role and
	// state. An error if a server carries NATS labels that make no sense.
	ListGroupMachines(ctx context.Context, group string) ([]model.GroupMember, error)
	// UpdateNATSMetadata sets the NATS labels of the server and leaves its other labels alone.
	UpdateNATSMetadata(ctx context.Context, serverID string, metadata model.NATSMetadata) error

	// NetworkZone is the network zone of a location ("eu-central" for fsn1). An error if the
	// location is unknown. Servers of one NATS group must all be in the same zone.
	NetworkZone(ctx context.Context, location string) (string, error)
	// EnsurePrivateNetwork creates the private network of the group, or finds it if it exists. The
	// name of the network is the same for every caller, so the provider's rule that names are
	// unique makes the create the lock of the first start: created is true for exactly one of
	// several callers at the same time, and that caller is the server candidate. It does not look
	// first: a network that exists is only found after the create was refused.
	// An error if a network of that name exists that viper did not create for the group, or whose
	// zone is not the zone of location.
	EnsurePrivateNetwork(ctx context.Context, group, location string) (net model.PrivateNetwork, created bool, err error)
	// FindPrivateNetwork returns the private network of the group, nil if there is none.
	FindPrivateNetwork(ctx context.Context, group string) (*model.PrivateNetwork, error)
	// AttachPrivateNetwork attaches an existing server to the network and returns its private IP;
	// a server that is attached already keeps its IP. (A new server is attached when it is created:
	// ServerSpec.Network.)
	AttachPrivateNetwork(ctx context.Context, serverID, networkID string) (privateIP string, err error)
	// DeletePrivateNetwork deletes the network; ErrNetworkInUse if a server is still attached.
	// A network that is gone already is no error.
	DeletePrivateNetwork(ctx context.Context, networkID string) error
}

// ErrNetworkInUse is returned by DeletePrivateNetwork for a network with servers attached.
var ErrNetworkInUse = errors.New("the private network still has servers attached")
