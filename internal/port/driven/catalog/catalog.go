// Package catalog is the driven port to the price list of a provider: which server types it has,
// what they offer and what they cost. An adapter per provider reads the provider's own API.
package catalog

import (
	"context"

	"github.com/pt9912/hetzner-viper/internal/model"
)

// Catalog is the price list of one provider.
type Catalog interface {
	// Offers lists the server types that can be ordered in the location, with their list prices.
	// A provider that lists all its locations at once (Hetzner) takes "" for all of them; one that
	// prices a region at a time (AWS, Azure, Google Cloud) needs the location.
	//
	// A provider whose system disk is chosen apart from the server type prices a disk of diskGB
	// into the offers (its default size for 0) and says so in Offer.SysDiskGB.
	Offers(ctx context.Context, location string, diskGB int) ([]model.Offer, error)
}
