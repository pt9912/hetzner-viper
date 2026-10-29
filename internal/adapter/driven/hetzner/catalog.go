package hetzner

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/pt9912/hetzner-viper/internal/model"
)

// Offers lists the server types of every location they can be ordered in. The prices are the
// list prices of the project with VAT, in the currency of the project (without the fee for the
// public IPv4 address).
func (h *Client) Offers(ctx context.Context, _ string, _ int) (offers []model.Offer, err error) {
	// the client library indexes the lists of the answer by the length of another one; an answer it
	// cannot read is an error, not a crash
	defer func() {
		if r := recover(); r != nil {
			offers, err = nil, fmt.Errorf("the answer to the price request cannot be read: %v", r)
		}
	}()
	pricing, _, err := h.c.Pricing.Get(ctx)
	if err != nil {
		return nil, err
	}
	types, err := h.c.ServerType.All(ctx)
	if err != nil {
		return nil, err
	}
	vat, _ := strconv.ParseFloat(pricing.VATRate, 64)
	ips := ipv4Prices(pricing)
	for _, t := range types {
		offers = append(offers, offersOf(t, pricing.Currency, vat, ips)...)
	}
	return offers, nil
}

// ipPrice is what a public IPv4 address costs in a location, with VAT.
type ipPrice struct{ hourly, monthly float64 }

// ipv4Prices are the prices of a primary IPv4 address by location.
func ipv4Prices(p hcloud.Pricing) map[string]ipPrice {
	out := map[string]ipPrice{}
	for _, pip := range p.PrimaryIPs {
		if pip.Type != "ipv4" {
			continue
		}
		for _, t := range pip.Pricings {
			hourly, err1 := strconv.ParseFloat(t.Hourly.Gross, 64)
			monthly, err2 := strconv.ParseFloat(t.Monthly.Gross, 64)
			if err1 == nil && err2 == nil {
				out[t.Location] = ipPrice{hourly: hourly, monthly: monthly}
			}
		}
	}
	return out
}

// offersOf is a server type once for each location it can be ordered in and has a price for.
func offersOf(t *hcloud.ServerType, currency string, vat float64, ips map[string]ipPrice) []model.Offer {
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
		// every machine gets a public IPv4 address; its fee is in the price
		ip, hasIP := ips[p.Location.Name]
		out = append(out, model.Offer{
			Provider: "hetzner", Type: t.Name, Location: p.Location.Name,
			VCPU: t.Cores, RAMGB: float64(t.Memory), DiskGB: t.Disk, Arch: arch,
			HourlyPrice: hourly + ip.hourly, MonthlyPrice: monthly + ip.monthly, Currency: currency, VATPercent: vat, IPv4: hasIP,
		})
	}
	return out
}
