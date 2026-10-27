// Package cloud is the driven port to a cloud provider: what viper needs of a provider (a server,
// a login key, a firewall for SSH), in the terms of internal/model. An adapter
// (internal/adapter/driven/hetzner, later others) translates it to the provider's API.
package cloud

import (
	"context"
	"fmt"

	"github.com/pt9912/hetzner-viper/internal/model"
)

// Describer is the part of a provider that words its failures for the user.
type Describer interface {
	// Name is the short name of the provider in messages ("Hetzner").
	Name() string
	// Describe is an error without the request details, safe to show.
	Describe(err error) string
	// Hint says what to check when the API cannot be used ("HCLOUD_TOKEN / project / network").
	Hint() string
}

// Provider is a cloud the commands create the server in.
type Provider interface {
	Describer
	// Title is the name of the provider as the preflight shows it ("Hetzner Cloud").
	Title() string

	// Validate reads (and changes nothing) to tell whether the server can be created.
	Validate(ctx context.Context, c model.Check) []model.Finding

	// ServerByName returns the server of that name, nil if there is none.
	ServerByName(ctx context.Context, name string) (*model.Server, error)
	// CreateServer creates the server and waits until it is running.
	CreateServer(ctx context.Context, s model.ServerSpec) (*model.Server, error)
	// DeleteServer deletes the server and waits until it is gone.
	DeleteServer(ctx context.Context, id string) error
	// EnsureLoginKey makes sure the public key is available under a Ref. *KeyConflictError if the
	// name is taken by another key; an *OpError for any other failure.
	EnsureLoginKey(ctx context.Context, name, publicKey string) (model.LoginKey, error)
	// EnsureFirewall makes sure the firewall allows SSH (TCP 22) and ping from the networks;
	// missing rules are added, other rules are left alone.
	EnsureFirewall(ctx context.Context, name string, sources []string) (model.FirewallResult, error)
}

// KeyConflictError is returned by EnsureLoginKey if a key of that name exists with other content.
type KeyConflictError struct{ Name string }

func (e *KeyConflictError) Error() string {
	return fmt.Sprintf("a key named %s exists with another public key", e.Name)
}

// OpError is a failed step of an EnsureX call. Op says what failed, e.g. "listing the SSH keys"
// (Write false: only read, so the credentials or the network are suspect) or "creating the
// firewall x" (Write true).
type OpError struct {
	Op    string
	Write bool
	Err   error
}

func (e *OpError) Error() string { return fmt.Sprintf("%s failed: %v", e.Op, e.Err) }
func (e *OpError) Unwrap() error { return e.Err }
