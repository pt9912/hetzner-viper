package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/pricecache"
	"github.com/pt9912/hetzner-viper/internal/model"
	"golang.org/x/sync/errgroup"
)

const (
	// retailPrices is the public price list of Azure; it needs no login.
	retailPrices = "https://prices.azure.com/api/retail/prices"
	// calculatorData is what the pricing calculator on the Azure website reads: the VM sizes with
	// cores, memory and prices. It is public but no documented API, so it is only the fallback for
	// the sizes if the subscription cannot tell them.
	calculatorData = "https://azure.microsoft.com/api/v3/pricing/virtual-machines/calculator/?culture=en-us&discount=mosp"

	// officialTimeout is how long the subscription has to give the sizes before the website is asked.
	officialTimeout = 30 * time.Second
)

// Spec is the size of a VM size.
type Spec struct {
	VCPUs int     `json:"vcpus"`
	RAMGB float64 `json:"ram_gb"`
}

// Specs gives the size of the VM sizes of a location, by the name that Key makes of theirs.
type Specs func(ctx context.Context, location string) (map[string]Spec, error)

// Key is the name of a VM size as the sources spell it alike: "Standard_D4as_v5" and "d4asv5" are
// the same size.
func Key(name string) string {
	return strings.ReplaceAll(strings.TrimPrefix(strings.ToLower(name), "standard_"), "_", "")
}

// Catalog is the price list of the Linux VMs of Azure. The prices are from the public Retail
// Prices API. The size of the VM sizes (cores, memory) is from the subscription (the API for the
// VM sizes of a location) if there is one and it answers, else from the data of the pricing
// calculator on the Azure website. What it digests is kept in the cache directory for a day.
type Catalog struct {
	official Specs // from the subscription; nil without one
	website  Specs
	http     *http.Client
	base     string // the price list's address, retailPrices unless a test says otherwise
	dir      string // the cache directory; "" for none
	now      func() time.Time
	notice   io.Writer // where it says what takes long or what it falls back to; nil for nowhere
}

// NewCatalog returns a catalog; subscriptionID may be "". It looks for the Azure credentials when
// it first needs them. dir is the cache directory ("" for none), notice where it says what it does.
func NewCatalog(subscriptionID, dir string, notice io.Writer) *Catalog {
	h := &http.Client{Timeout: 5 * time.Minute}
	c := &Catalog{http: h, base: retailPrices, dir: dir, now: time.Now, notice: notice}
	c.website = c.calculatorSpecs(calculatorData)
	if subscriptionID != "" {
		c.official = sdkSpecs(subscriptionID)
	}
	return c
}

// NewCatalogWith returns a catalog over given sources of sizes and a given price list (for tests).
func NewCatalogWith(official, website Specs, h *http.Client, base, dir string, now func() time.Time, notice io.Writer) *Catalog {
	return &Catalog{official: official, website: website, http: h, base: base, dir: dir, now: now, notice: notice}
}

func sdkSpecs(subscriptionID string) Specs {
	return func(ctx context.Context, location string) (map[string]Spec, error) {
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, err
		}
		c, err := armcompute.NewVirtualMachineSizesClient(subscriptionID, cred, nil)
		if err != nil {
			return nil, err
		}
		out := map[string]Spec{}
		pager := c.NewListPager(location, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, s := range page.Value {
				out[Key(str(s.Name))] = Spec{VCPUs: int(i32(s.NumberOfCores)), RAMGB: float64(i32(s.MemoryInMB)) / 1024}
			}
		}
		return out, nil
	}
}

// Describe is an API error without the request details, safe to show.
func (*Catalog) Describe(err error) string { return (*Client)(nil).Describe(err) }

// digest is what the catalog keeps of the price lists of a location.
type digest struct {
	Fetched  time.Time `json:"fetched"`
	Currency string    `json:"currency"`
	// IPv4 is the fee for a standard public IPv4 address per hour.
	IPv4  float64 `json:"ipv4"`
	Types []entry `json:"types"`
	// SSD are the Standard SSD tiers with their price per month.
	SSD []tier `json:"ssd"`
}

// entry is a VM size with its pay-as-you-go price per hour.
type entry struct {
	Name   string  `json:"name"`
	VCPUs  int     `json:"vcpus"`
	RAMGB  float64 `json:"ram_gb"`
	Hourly float64 `json:"hourly"`
}

type tier struct {
	GB      int     `json:"gb"`
	Monthly float64 `json:"monthly"`
}

// validLocation is what a location looks like; it is also a part of the name of the cache file.
var validLocation = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Offers lists the VM sizes of the location that have a price, with the price of a Standard SSD
// system disk of diskGB (64 GB for 0) added. The prices are in euro without VAT.
func (c *Catalog) Offers(ctx context.Context, location string, diskGB int) ([]model.Offer, error) {
	if !validLocation.MatchString(location) {
		return nil, fmt.Errorf("no location: give --location or SERVER_LOCATION (germanywestcentral)")
	}
	d, err := c.digestOf(ctx, location)
	if err != nil {
		return nil, err
	}
	if diskGB <= 0 {
		diskGB = int(Options{}.diskGB())
	}
	disk, ok := d.diskPrice(diskGB)
	if !ok {
		return nil, fmt.Errorf("no price of Standard SSD disks of %d GB in %s", diskGB, location)
	}
	out := make([]model.Offer, 0, len(d.Types))
	for _, t := range d.Types {
		arch := "x86"
		if armSize(t.Name) {
			arch = "arm"
		}
		out = append(out, model.Offer{
			Provider: "azure", Type: t.Name, Location: location, VCPU: t.VCPUs, RAMGB: t.RAMGB, Arch: arch, SysDiskGB: diskGB,
			HourlyPrice: t.Hourly + d.IPv4 + disk/model.HoursPerMonth, MonthlyPrice: (t.Hourly+d.IPv4)*model.HoursPerMonth + disk, Currency: d.Currency, IPv4: true,
		})
	}
	return out, nil
}

// diskPrice is the price per month of the smallest Standard SSD tier that holds diskGB.
func (d *digest) diskPrice(diskGB int) (float64, bool) {
	best, bestGB := 0.0, 0
	for _, t := range d.SSD {
		if t.GB >= diskGB && (bestGB == 0 || t.GB < bestGB) {
			best, bestGB = t.Monthly, t.GB
		}
	}
	return best, bestGB != 0
}

// digestOf is the digest of the location: the cached one if it is young enough, else a new one. If
// the price lists cannot be read, an old digest is better than none.
func (c *Catalog) digestOf(ctx context.Context, location string) (*digest, error) {
	return pricecache.Get(ctx, c.dir, "azure-"+location+".json", c.now,
		func(d *digest) time.Time { return d.Fetched },
		func(d *digest) bool { return len(d.Types) > 0 && len(d.SSD) > 0 && d.IPv4 > 0 },
		func(ctx context.Context) (*digest, error) { return c.fetch(ctx, location) })
}

// fetch reads the sizes and the two price lists, at the same time as they do not depend on each other.
func (c *Catalog) fetch(ctx context.Context, location string) (*digest, error) {
	var (
		specs                     map[string]Spec
		vmItems, ssdItem, ipItems []priceItem
		g, gc                     = errgroup.WithContext(ctx)
	)
	g.Go(func() (err error) { specs, err = c.specs(gc, location); return err })
	g.Go(func() (err error) { vmItems, err = c.items(gc, filterOf("Virtual Machines", "", location)); return err })
	g.Go(func() (err error) {
		ssdItem, err = c.items(gc, filterOf("Storage", "Standard SSD Managed Disks", location))
		return err
	})
	g.Go(func() (err error) {
		ipItems, err = c.items(gc, filterOf("Virtual Network", "IP Addresses", location))
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}
	d := &digest{Fetched: c.now(), SSD: ssdTiers(ssdItem), IPv4: ipv4Price(ipItems)}
	prices, currency := hourlyPrices(vmItems)
	d.Currency = currency
	for name, p := range prices {
		if s, ok := specs[Key(name)]; ok && s.VCPUs > 0 && s.RAMGB > 0 {
			d.Types = append(d.Types, entry{Name: name, VCPUs: s.VCPUs, RAMGB: s.RAMGB, Hourly: p})
		}
	}
	switch {
	case len(d.Types) == 0:
		return nil, fmt.Errorf("no VM sizes with a price in %s", location)
	case len(d.SSD) == 0:
		return nil, fmt.Errorf("no price of Standard SSD disks in %s", location)
	case d.IPv4 <= 0:
		return nil, fmt.Errorf("no price of public IPv4 addresses in %s", location)
	}
	return d, nil
}

// specs is the size of the VM sizes: from the subscription if there is one and it answers, else
// from the website.
func (c *Catalog) specs(ctx context.Context, location string) (map[string]Spec, error) {
	if c.official != nil {
		// the chain of credentials asks the environment, the managed identity and the CLI one after
		// the other: without a login that takes a while, and the website has the answer anyway
		octx, cancel := context.WithTimeout(ctx, officialTimeout)
		defer cancel()
		s, err := c.official(octx, location)
		if err == nil && len(s) == 0 {
			err = fmt.Errorf("no VM sizes")
		}
		if err == nil {
			return s, nil
		}
		if c.notice != nil {
			fmt.Fprintf(c.notice, "azure: the sizes of the subscription are not available (%s); using the data of the Azure website\n", c.Describe(err))
		}
	}
	return c.website(ctx, location)
}

// specsCache keeps the sizes from the website, which are the same for every location.
type specsCache struct {
	Fetched time.Time       `json:"fetched"`
	Specs   map[string]Spec `json:"specs"`
}

// calculatorSpecs reads the sizes from the data of the pricing calculator, once a day.
func (c *Catalog) calculatorSpecs(addr string) Specs {
	return func(ctx context.Context, _ string) (map[string]Spec, error) {
		cached, err := pricecache.Get(ctx, c.dir, "azure-specs.json", c.now,
			func(s *specsCache) time.Time { return s.Fetched },
			func(s *specsCache) bool { return len(s.Specs) > 0 },
			func(ctx context.Context) (*specsCache, error) {
				if c.notice != nil {
					fmt.Fprintln(c.notice, "azure: reading the VM sizes from the Azure website (a large file, once a day) ...")
				}
				specs, err := c.readCalculator(ctx, addr)
				if err != nil {
					return nil, err
				}
				return &specsCache{Fetched: c.now(), Specs: specs}, nil
			})
		if err != nil {
			return nil, err
		}
		return cached.Specs, nil
	}
}

// calculator is the part of the data of the pricing calculator the catalog reads: the offers by
// name ("linux-d4asv5-standard"), of which it takes the standard Linux ones.
type calculator struct {
	Offers map[string]struct {
		Cores float64 `json:"cores"`
		RAM   float64 `json:"ram"`
	} `json:"offers"`
}

func (c *Catalog) readCalculator(ctx context.Context, addr string) (map[string]Spec, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ue, ok := err.(*url.Error); ok {
			err = ue.Err
		}
		return nil, fmt.Errorf("VM sizes: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // read only
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("VM sizes: HTTP %d", resp.StatusCode)
	}
	var data calculator
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("VM sizes: %w", err)
	}
	out := map[string]Spec{}
	for name, o := range data.Offers {
		size, ok := strings.CutPrefix(name, "linux-")
		if size, ok2 := strings.CutSuffix(size, "-standard"); ok && ok2 && o.Cores >= 1 && o.RAM > 0 {
			out[size] = Spec{VCPUs: int(o.Cores), RAMGB: o.RAM}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("VM sizes: none in the data")
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

// hourlyPrices are the pay-as-you-go prices of the Linux VMs by size name, and their currency.
func hourlyPrices(items []priceItem) (map[string]float64, string) {
	prices, currency := map[string]float64{}, ""
	for _, it := range items {
		if !payAsYouGoLinux(it) {
			continue
		}
		if cur, ok := prices[it.ArmSkuName]; !ok || it.RetailPrice < cur {
			prices[it.ArmSkuName] = it.RetailPrice
		}
		currency = it.CurrencyCode
	}
	return prices, currency
}

// ssdSizes are the sizes of the Standard SSD disks by name of the tier.
func ssdSizes() map[string]int {
	return map[string]int{
		"E1": 4, "E2": 8, "E3": 16, "E4": 32, "E6": 64, "E10": 128, "E15": 256, "E20": 512, "E30": 1024,
		"E40": 2048, "E50": 4096, "E60": 8192, "E70": 16384, "E80": 32767,
	}
}

// ssdTiers are the locally redundant Standard SSD tiers of the price list with their monthly price.
func ssdTiers(items []priceItem) []tier {
	sizes := ssdSizes()
	var out []tier
	for _, it := range items {
		name, _, _ := strings.Cut(it.SkuName, " ")
		if gb, ok := sizes[name]; ok && strings.HasSuffix(it.MeterName, " LRS Disk") && it.UnitOfMeasure == "1/Month" && it.RetailPrice > 0 {
			out = append(out, tier{GB: gb, Monthly: it.RetailPrice})
		}
	}
	return out
}

// ipv4Price is the fee per hour for a standard static public IPv4 address, the kind viper creates.
func ipv4Price(items []priceItem) float64 {
	for _, it := range items {
		if it.MeterName == "Standard IPv4 Static Public IP" && it.UnitOfMeasure == "1 Hour" && it.RetailPrice > 0 {
			return it.RetailPrice
		}
	}
	return 0
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
