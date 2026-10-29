package hetzner

import (
	"context"
	"strconv"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/pt9912/hetzner-viper/internal/model"
)

// Offers lists the server types of every location they can be ordered in. The prices are the
// list prices of the project with VAT, in the currency of the project (without the fee for the
// public IPv4 address).
func (h *Client) Offers(ctx context.Context) ([]model.Offer, error) {
	pricing, _, err := h.c.Pricing.Get(ctx)
	if err != nil {
		return nil, err
	}
	types, err := h.c.ServerType.All(ctx)
	if err != nil {
		return nil, err
	}
	vat, _ := strconv.ParseFloat(pricing.VATRate, 64)
	var out []model.Offer
	for _, t := range types {
		out = append(out, offersOf(t, pricing.Currency, vat)...)
	}
	return out, nil
}

// offersOf is a server type once for each location it can be ordered in and has a price for.
func offersOf(t *hcloud.ServerType, currency string, vat float64) []model.Offer {
	arch := string(t.Architecture)
	if arch == "" {
		arch = "x86"
	}
	orderable := map[string]bool{}
	for _, l := range t.Locations {
		if l.Available && l.Location != nil {
			orderable[l.Location.Name] = true
		}
	}
	var out []model.Offer
	for _, p := range t.Pricings {
		if p.Location == nil || !orderable[p.Location.Name] {
			continue
		}
		hourly, err1 := strconv.ParseFloat(p.Hourly.Gross, 64)
		monthly, err2 := strconv.ParseFloat(p.Monthly.Gross, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, model.Offer{
			Provider: "hetzner", Type: t.Name, Location: p.Location.Name,
			VCPU: t.Cores, RAMGB: float64(t.Memory), DiskGB: t.Disk, Arch: arch,
			HourlyPrice: hourly, MonthlyPrice: monthly, Currency: currency, VATPercent: vat,
		})
	}
	return out
}
