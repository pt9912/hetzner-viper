package hetzner

import (
	"context"
	"fmt"
	"io"

	"github.com/pt9912/hetzner-viper/internal/model"
)

// Catalog is the price list of Hetzner: from the project if there is a token and it works, which gives
// the exact prices with the VAT of the project, else from the public pages of the website.
type Catalog struct {
	api    *Client // nil without a token
	public *Public
	notice io.Writer
}

// NewCatalog returns a catalog; token may be "". dir is the cache directory ("" for none), notice
// where it says what it does.
func NewCatalog(token, dir string, notice io.Writer) *Catalog {
	c := &Catalog{public: NewPublic(dir, notice), notice: notice}
	if token != "" {
		c.api = New(token, "")
	}
	return c
}

// NewCatalogWith returns a catalog over given sources (for tests); api may be nil.
func NewCatalogWith(api *Client, public *Public, notice io.Writer) *Catalog {
	return &Catalog{api: api, public: public, notice: notice}
}

// Offers lists the server types of the location ("" for all). If the project cannot be read, the pages
// are the fallback, and the notice says why.
func (c *Catalog) Offers(ctx context.Context, location string, diskGB int) ([]model.Offer, error) {
	if c.api != nil {
		offers, err := c.api.Offers(ctx, location, diskGB)
		if err == nil || ctx.Err() != nil {
			return offers, err
		}
		if c.notice != nil {
			fmt.Fprintf(c.notice, "hetzner: the project cannot be read (%s); using the public pricing pages, whose prices are without VAT\n", Error(err))
		}
	}
	return c.public.Offers(ctx, location, diskGB)
}
