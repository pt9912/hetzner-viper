package gcp_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/gcp"
	"google.golang.org/api/cloudbilling/v1"
	"google.golang.org/api/compute/v1"
)

func sku(desc, usage, region string, units, nanos int64) *cloudbilling.Sku {
	return &cloudbilling.Sku{
		Description: desc, Category: &cloudbilling.Category{UsageType: usage}, ServiceRegions: []string{region},
		PricingInfo: []*cloudbilling.PricingInfo{{PricingExpression: &cloudbilling.PricingExpression{TieredRates: []*cloudbilling.TierRate{{UnitPrice: &cloudbilling.Money{CurrencyCode: "EUR", Units: units, Nanos: nanos}}}}}},
	}
}

func TestCatalogPricesTheCoresAndTheMemory(t *testing.T) {
	types := func(_ context.Context, zone string) ([]*compute.MachineType, error) {
		if zone != "europe-west3-b" {
			t.Errorf("zone %q", zone)
		}
		return []*compute.MachineType{
			{Name: "e2-standard-4", GuestCpus: 4, MemoryMb: 16384},
			{Name: "t2a-standard-4", GuestCpus: 4, MemoryMb: 16384, Architecture: "ARM64"},
			{Name: "e2-micro", GuestCpus: 2, MemoryMb: 1024, IsSharedCpu: true},
			{Name: "c3-standard-4-lssd", GuestCpus: 4, MemoryMb: 16384},
			{Name: "a2-highgpu-1g", GuestCpus: 12, MemoryMb: 87040},                                                                              // a family without a SKU of cores and memory
			{Name: "e2-with-gpu-1", GuestCpus: 4, MemoryMb: 16384, Accelerators: []*compute.MachineTypeAccelerators{{GuestAcceleratorCount: 1}}}, // billed apart
			{Name: "g2-standard-16", GuestCpus: 16, MemoryMb: 65536},                                                                             // GPU by its family
		}, nil
	}
	skus := func(_ context.Context, currency string) ([]*cloudbilling.Sku, error) {
		if currency != "EUR" {
			t.Errorf("currency %q", currency)
		}
		return []*cloudbilling.Sku{
			sku("G2 Instance Core running in Frankfurt", "OnDemand", "europe-west3", 0, 25_000_000),
			sku("G2 Instance Ram running in Frankfurt", "OnDemand", "europe-west3", 0, 10_000_000),
			sku("E2 Instance Core running in Frankfurt", "OnDemand", "europe-west3", 0, 25_000_000),
			sku("E2 Instance Ram running in Frankfurt", "OnDemand", "europe-west3", 0, 10_000_000),
			sku("E2 Instance Core running in Frankfurt", "Preemptible", "europe-west3", 0, 1_000_000),
			sku("E2 Instance Core running in Iowa", "OnDemand", "us-central1", 0, 99_000_000),
			sku("T2A Arm Instance Core running in Frankfurt", "OnDemand", "europe-west3", 0, 30_000_000),
			sku("T2A Arm Instance Ram running in Frankfurt", "OnDemand", "europe-west3", 0, 5_000_000),
			sku("Balanced PD Capacity in Frankfurt", "OnDemand", "europe-west3", 0, 100_000_000),
			sku("Regional Balanced PD Capacity in Frankfurt", "OnDemand", "europe-west3", 9, 0),
			sku("N2 Custom Instance Core running in Frankfurt", "OnDemand", "europe-west3", 9, 0),
			sku("Commitment v1: E2 Cpu in Frankfurt for 1 Year", "Commit1Yr", "europe-west3", 9, 0),
		}, nil
	}
	offers, err := gcp.NewCatalogWith(types, skus).Offers(context.Background(), "europe-west3-b", 100)
	if err != nil || len(offers) != 2 {
		t.Fatalf("%+v %v", offers, err)
	}
	sort.Slice(offers, func(i, j int) bool { return offers[i].Type < offers[j].Type })
	e2, t2a := offers[0], offers[1]
	// 4 cores x 0.025 + 16 GiB x 0.010 + 100 GB x 0.10 a month
	if e2.Type != "e2-standard-4" || e2.Provider != "gcp" || e2.Location != "europe-west3-b" || e2.VCPU != 4 || e2.RAMGB != 16 || e2.Arch != "x86" || e2.DiskGB != 0 || e2.Currency != "EUR" || e2.VATPercent != 0 || e2.SysDiskGB != 100 || abs(e2.HourlyPrice-(0.26+100*0.10/730)) > 1e-9 {
		t.Errorf("%+v", e2)
	}
	if t2a.Arch != "arm" || abs(t2a.HourlyPrice-(4*0.03+16*0.005+100*0.10/730)) > 1e-9 {
		t.Errorf("%+v", t2a)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestCatalogNeedsAZoneAndPassesErrors(t *testing.T) {
	ok := func(context.Context, string) ([]*compute.MachineType, error) { return nil, nil }
	bad := func(context.Context, string) ([]*cloudbilling.Sku, error) {
		return nil, errors.New("billing api disabled")
	}
	for _, loc := range []string{"", "europe-west3"} {
		if _, err := gcp.NewCatalogWith(ok, bad).Offers(context.Background(), loc, 0); err == nil || !strings.Contains(err.Error(), "zone") {
			t.Errorf("%q: %v", loc, err)
		}
	}
	if _, err := gcp.NewCatalogWith(ok, bad).Offers(context.Background(), "europe-west3-b", 100); err == nil || err.Error() != "billing api disabled" {
		t.Errorf("%v", err)
	}
}
