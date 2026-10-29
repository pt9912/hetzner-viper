package gcp

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
	"google.golang.org/api/cloudbilling/v1"
	"google.golang.org/api/compute/v1"
)

// computeEngine is the service of Compute Engine in the Cloud Billing catalog.
const computeEngine = "services/6F81-5844-456A"

// MachineTypes lists the machine types of a zone.
type MachineTypes func(ctx context.Context, zone string) ([]*compute.MachineType, error)

// Skus lists the SKUs of Compute Engine, with the prices in a currency.
type Skus func(ctx context.Context, currency string) ([]*cloudbilling.Sku, error)

// Catalog is the price list of the predefined machine types: the types of a zone from the project,
// their price from the Cloud Billing catalog. Compute Engine prices the cores and the memory of a
// family apart, not the machine type, so the price of a type is cores x core price plus GiB x
// memory price (what the pricing page does).
type Catalog struct {
	types MachineTypes
	skus  Skus
}

// NewCatalog returns a catalog of the project; it looks for the credentials when it is first used.
func NewCatalog(project string) *Catalog {
	return &Catalog{types: sdkMachineTypes(project), skus: sdkSkus}
}

// NewCatalogWith returns a catalog over given lists (for tests).
func NewCatalogWith(t MachineTypes, s Skus) *Catalog { return &Catalog{types: t, skus: s} }

func sdkMachineTypes(project string) MachineTypes {
	return func(ctx context.Context, zone string) ([]*compute.MachineType, error) {
		svc, err := compute.NewService(ctx)
		if err != nil {
			return nil, err
		}
		var out []*compute.MachineType
		err = svc.MachineTypes.List(project, zone).Pages(ctx, func(p *compute.MachineTypeList) error {
			out = append(out, p.Items...)
			return nil
		})
		return out, err
	}
}

func sdkSkus(ctx context.Context, currency string) ([]*cloudbilling.Sku, error) {
	svc, err := cloudbilling.NewService(ctx)
	if err != nil {
		return nil, err
	}
	var out []*cloudbilling.Sku
	err = svc.Services.Skus.List(computeEngine).CurrencyCode(currency).Pages(ctx, func(p *cloudbilling.ListSkusResponse) error {
		out = append(out, p.Skus...)
		return nil
	})
	return out, err
}

// Describe is an API error without the request details, safe to show.
func (*Catalog) Describe(err error) string { return (*Client)(nil).Describe(err) }

// zoneRe is a zone: the region and the letter.
var zoneRe = regexp.MustCompile(`^([a-z]+-[a-z]+[0-9]+)-[a-z]$`)

// skuName reads the family from the description of a SKU of the cores or the memory of a
// predefined family: "N2D AMD Instance Core running in Frankfurt" is n2d, core.
var skuName = regexp.MustCompile(`^([A-Za-z0-9]+)(?: (?:AMD|Arm|Predefined))? Instance (Core|Ram) running in `)

// rates are what a family's cores and memory cost per hour.
type rates struct{ core, ram float64 }

// Offers lists the machine types of the zone whose family has a price, in the zone's region, with
// the price of a balanced boot disk of diskGB (50 GB for 0) added. The prices are in euro without
// VAT. The shared-core types and the types with local SSDs are left out.
func (c *Catalog) Offers(ctx context.Context, location string, diskGB int) ([]model.Offer, error) {
	m := zoneRe.FindStringSubmatch(location)
	if m == nil {
		return nil, fmt.Errorf("no zone: give --location or SERVER_LOCATION as a zone (europe-west3-b)")
	}
	types, err := c.types(ctx, location)
	if err != nil {
		return nil, err
	}
	skus, err := c.skus(ctx, "EUR")
	if err != nil {
		return nil, err
	}
	byFamily, currency := familyRates(skus, m[1])
	disks := diskRates(skus, m[1])
	if diskGB <= 0 {
		diskGB = int(Options{}.diskGB())
	}
	var out []model.Offer
	for _, t := range types {
		family, _, _ := strings.Cut(t.Name, "-")
		r, ok := byFamily[family]
		if !ok || t.IsSharedCpu || t.Deprecated != nil || strings.Contains(t.Name, "lssd") || strings.Contains(t.Name, "custom") || r.core == 0 || r.ram == 0 {
			continue
		}
		perGB, ok := disks[diskType(t.Name)]
		if !ok {
			continue
		}
		disk := float64(diskGB) * perGB // per month
		hourly := float64(t.GuestCpus)*r.core + float64(t.MemoryMb)/1024*r.ram + disk/model.HoursPerMonth
		arch := "x86"
		if t.Architecture == "ARM64" {
			arch = "arm"
		}
		out = append(out, model.Offer{
			Provider: "gcp", Type: t.Name, Location: location, VCPU: int(t.GuestCpus), RAMGB: float64(t.MemoryMb) / 1024, Arch: arch, SysDiskGB: diskGB,
			HourlyPrice: hourly, MonthlyPrice: hourly * model.HoursPerMonth, Currency: currency,
		})
	}
	return out, nil
}

// familyRates are the on-demand prices of the cores and the memory of each family in a region.
func familyRates(skus []*cloudbilling.Sku, region string) (map[string]rates, string) {
	out := map[string]rates{}
	currency := ""
	for _, s := range skus {
		m := skuName.FindStringSubmatch(s.Description)
		if m == nil || s.Category == nil || s.Category.UsageType != "OnDemand" || !slices.Contains(s.ServiceRegions, region) {
			continue
		}
		price, cur, ok := unitPrice(s)
		if !ok {
			continue
		}
		currency = cur
		family := strings.ToLower(m[1])
		r := out[family]
		if m[2] == "Core" {
			r.core = price
		} else {
			r.ram = price
		}
		out[family] = r
	}
	return out, currency
}

// diskRates are the prices per GiB and month of the boot disk types by name of the type.
func diskRates(skus []*cloudbilling.Sku, region string) map[string]float64 {
	out := map[string]float64{}
	for _, s := range skus {
		if s.Category == nil || s.Category.UsageType != "OnDemand" || !slices.Contains(s.ServiceRegions, region) {
			continue
		}
		var typ string
		switch {
		case strings.HasPrefix(s.Description, "Balanced PD Capacity"):
			typ = "pd-balanced"
		case strings.HasPrefix(s.Description, "Hyperdisk Balanced Capacity"):
			typ = "hyperdisk-balanced"
		default:
			continue
		}
		if price, _, ok := unitPrice(s); ok {
			out[typ] = price
		}
	}
	return out
}

// unitPrice is the price of the first tier of a SKU, per core hour or GiB hour.
func unitPrice(s *cloudbilling.Sku) (price float64, currency string, ok bool) {
	if len(s.PricingInfo) == 0 || s.PricingInfo[0].PricingExpression == nil || len(s.PricingInfo[0].PricingExpression.TieredRates) == 0 {
		return 0, "", false
	}
	u := s.PricingInfo[0].PricingExpression.TieredRates[0].UnitPrice
	if u == nil {
		return 0, "", false
	}
	return float64(u.Units) + float64(u.Nanos)/1e9, u.CurrencyCode, true
}
