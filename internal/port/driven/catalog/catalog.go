// Package catalog is the driven port to the price list of a provider: which server types it has,
// what they offer and what they cost. An adapter per provider reads the provider's own API.
package catalog

import (
	"context"

	"github.com/pt9912/hetzner-viper/internal/model"
)

// Catalog is the price list of one provider.
type Catalog interface {
	// Offers lists the server types of every location they can be ordered in, with their list
	// prices. Types that cannot be ordered are left out.
	Offers(ctx context.Context) ([]model.Offer, error)
}
