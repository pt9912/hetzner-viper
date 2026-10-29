package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/pt9912/hetzner-viper/internal/model"
	"golang.org/x/sync/errgroup"
)

// retailPrices is the public price list of Azure; it needs no login.
const retailPrices = "https://prices.azure.com/api/retail/prices"

// SizeInfo is what the VM sizes of a location say of one size.
type SizeInfo struct {
	Name     string
	VCPUs    int
	MemoryMB int
}

// Sizes lists the VM sizes of a location.
type Sizes func(ctx context.Context, location string) ([]SizeInfo, error)

// Catalog is the price list of the Linux VMs of Azure: the sizes of a location from the
// subscription, their pay-as-you-go prices from the public Retail Prices API.
type Catalog struct {
	sizes Sizes
	http  *http.Client
	base  string // the price list's address, retailPrices unless a test says otherwise
}

// NewCatalog returns a catalog; it looks for the Azure credentials when it is first used.
func NewCatalog(subscriptionID string) *Catalog {
	return &Catalog{sizes: sdkSizes(subscriptionID), http: &http.Client{Timeout: 60 * time.Second}, base: retailPrices}
}

// NewCatalogWith returns a catalog over given sizes and a given price list (for tests).
func NewCatalogWith(sizes Sizes, h *http.Client, base string) *Catalog {
	return &Catalog{sizes: sizes, http: h, base: base}
}

func sdkSizes(subscriptionID string) Sizes {
	return func(ctx context.Context, location string) ([]SizeInfo, error) {
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, err
		}
		c, err := armcompute.NewVirtualMachineSizesClient(subscriptionID, cred, nil)
		if err != nil {
			return nil, err
		}
		var out []SizeInfo
		pager := c.NewListPager(location, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, s := range page.Value {
				out = append(out, SizeInfo{Name: str(s.Name), VCPUs: int(i32(s.NumberOfCores)), MemoryMB: int(i32(s.MemoryInMB))})
			}
		}
		return out, nil
	}
}

// Describe is an API error without the request details, safe to show.
func (*Catalog) Describe(err error) string { return (*Client)(nil).Describe(err) }

// Offers lists the VM sizes of the location that have a price, with the price of a Standard SSD
// system disk of diskGB (64 GB for 0) added. The prices are in euro without VAT.
func (c *Catalog) Offers(ctx context.Context, location string, diskGB int) ([]model.Offer, error) {
	if location == "" {
		return nil, fmt.Errorf("no location: give --location or SERVER_LOCATION")
	}
	if diskGB <= 0 {
		diskGB = int(Options{}.diskGB())
	}
	// the sizes and the two price lists do not depend on each other
	var (
		sizes            []SizeInfo
		vmItems, ssdItem []priceItem
		g, gc            = errgroup.WithContext(ctx)
	)
	g.Go(func() (err error) { sizes, err = c.sizes(gc, location); return err })
	g.Go(func() (err error) { vmItems, err = c.items(gc, filterOf("Virtual Machines", "", location)); return err })
	g.Go(func() (err error) {
		ssdItem, err = c.items(gc, filterOf("Storage", "Standard SSD Managed Disks", location))
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}
	prices, currency := hourlyPrices(vmItems)
	disk, err := diskPrice(ssdItem, diskGB)
	if err != nil {
		return nil, fmt.Errorf("%w in %s", err, location)
	}
	var out []model.Offer
	for _, s := range sizes {
		p, ok := prices[strings.ToLower(s.Name)]
		if !ok {
			continue
		}
		arch := "x86"
		if armSize(s.Name) {
			arch = "arm"
		}
		out = append(out, model.Offer{
			Provider: "azure", Type: s.Name, Location: location, VCPU: s.VCPUs, RAMGB: float64(s.MemoryMB) / 1024, Arch: arch, SysDiskGB: diskGB,
			HourlyPrice: p + disk/model.HoursPerMonth, MonthlyPrice: p*model.HoursPerMonth + disk, Currency: currency,
		})
	}
	return out, nil
}

type priceItem struct {
	CurrencyCode  string  `json:"currencyCode"`
	RetailPrice   float64 `json:"retailPrice"`
	UnitOfMeasure string  `json:"unitOfMeasure"`
	ArmSkuName    string  `json:"armSkuName"`
	ProductName   string  `json:"productName"`
	SkuName       string  `json:"skuName"`
	MeterName     string  `json:"meterName"`
	Type          string  `json:"type"`
}

type pricePage struct {
	Items    []priceItem `json:"Items"`
	NextPage string      `json:"NextPageLink"`
}

// filterOf is the filter of the price list for a service (and a product) in a region.
func filterOf(service, product, location string) string {
	f := fmt.Sprintf("serviceName eq '%s' and priceType eq 'Consumption' and armRegionName eq '%s'", service, strings.ReplaceAll(location, "'", ""))
	if product != "" {
		f += fmt.Sprintf(" and productName eq '%s'", product)
	}
	return f
}

// items reads all the pages of the price list for a filter, in euro.
func (c *Catalog) items(ctx context.Context, filter string) ([]priceItem, error) {
	var out []priceItem
	next := c.base + "?currencyCode=EUR&$filter=" + url.QueryEscape(filter)
	for next != "" {
		page, err := c.page(ctx, next)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
		next = page.NextPage
	}
	return out, nil
}

// hourlyPrices are the pay-as-you-go prices of the Linux VMs by lower-case size name, and their currency.
func hourlyPrices(items []priceItem) (map[string]float64, string) {
	prices, currency := map[string]float64{}, ""
	for _, it := range items {
		if !payAsYouGoLinux(it) {
			continue
		}
		key := strings.ToLower(it.ArmSkuName)
		if cur, ok := prices[key]; !ok || it.RetailPrice < cur {
			prices[key] = it.RetailPrice
		}
		currency = it.CurrencyCode
	}
	return prices, currency
}

// ssdTiers are the sizes of the Standard SSD disks by name of the tier; a disk costs what the
// smallest tier it fits in costs.
func ssdTiers() map[string]int {
	return map[string]int{
		"E1": 4, "E2": 8, "E3": 16, "E4": 32, "E6": 64, "E10": 128, "E15": 256, "E20": 512, "E30": 1024,
		"E40": 2048, "E50": 4096, "E60": 8192, "E70": 16384, "E80": 32767,
	}
}

// diskPrice is the price per month of a locally redundant Standard SSD that holds diskGB.
func diskPrice(items []priceItem, diskGB int) (float64, error) {
	best, bestSize := 0.0, 0
	tiers := ssdTiers()
	for _, it := range items {
		tier, _, _ := strings.Cut(it.SkuName, " ")
		size, ok := tiers[tier]
		if !ok || !strings.HasSuffix(it.MeterName, " LRS Disk") || it.UnitOfMeasure != "1/Month" || size < diskGB {
			continue
		}
		if bestSize == 0 || size < bestSize {
			best, bestSize = it.RetailPrice, size
		}
	}
	if bestSize == 0 {
		return 0, fmt.Errorf("no price of Standard SSD disks of %d GB", diskGB)
	}
	return best, nil
}

// payAsYouGoLinux tells the plain Linux price of a size from the Windows, spot and low-priority ones.
func payAsYouGoLinux(it priceItem) bool {
	return it.Type == "Consumption" && it.UnitOfMeasure == "1 Hour" && it.ArmSkuName != "" && it.RetailPrice > 0 &&
		!strings.Contains(it.ProductName, "Windows") && !strings.Contains(it.SkuName, "Spot") && !strings.Contains(it.SkuName, "Low Priority")
}

func (c *Catalog) page(ctx context.Context, addr string) (*pricePage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ue, ok := err.(*url.Error); ok {
			err = ue.Err
		}
		return nil, fmt.Errorf("price list: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // read only
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("price list: HTTP %d", resp.StatusCode)
	}
	var p pricePage
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return nil, fmt.Errorf("price list: %w", err)
	}
	return &p, nil
}
