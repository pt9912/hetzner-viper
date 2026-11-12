package cloud

import "context"

// Leftovers is what a provider can have left of a machine that has no server any more: a pod machine whose
// StatefulSet was removed by hand or by an interrupted viper down leaves its Secret and its volumes, with the data on
// them. It is a capability apart from Provider (ask with As); viper down uses it when the server is not found.
type Leftovers interface {
	// Leftovers names what is left of the machine (for the user to read), nothing if there is none or if the machine exists.
	Leftovers(ctx context.Context, name string) ([]string, error)
	// DeleteLeftovers deletes it; the machine must not exist.
	DeleteLeftovers(ctx context.Context, name string) error
}
