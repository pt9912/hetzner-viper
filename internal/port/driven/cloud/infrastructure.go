package cloud

import (
	"context"
	"io"
)

// Infrastructure is what a provider adds that makes something for its machines beyond the machines (a k3d cluster and its
// registry) and takes it away again with the last one. It is a capability apart from Provider (ask with As). It touches
// only what viper made itself.
type Infrastructure interface {
	// InfrastructureLeft names what viper made and what goes with the last machine. With a machine named, that machine is
	// still counted (the question before viper down); with "" the machines are gone (what to remove after the delete).
	// Nothing if more machines remain or if viper made nothing.
	InfrastructureLeft(ctx context.Context, machine string) []InfrastructureItem
	// RemoveInfrastructure removes the items with these keys, if no machine is left, and says on out what it removed.
	RemoveInfrastructure(ctx context.Context, out io.Writer, keys []string) error
}

// InfrastructureItem is one thing that goes with the last machine.
type InfrastructureItem struct {
	// Key names it to RemoveInfrastructure.
	Key string
	// What says it in words ("the k3d cluster viper").
	What string
	// Optional is true if the user decides whether it goes (a registry holds an image that other clusters can use); the
	// rest goes with the last machine.
	Optional bool
}
