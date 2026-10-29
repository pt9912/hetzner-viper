package aws

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
)

// bulkBase is where AWS publishes the price lists of EC2, one file per region. They are public:
// they need no account and no login.
const bulkBase = "https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonEC2/current/"

// cacheMaxAge is how long the digest of a price list is used before the list is read again.
const cacheMaxAge = 24 * time.Hour

// Catalog is the price list of the on-demand Linux instances of EC2. The list of a region is a
// file of several hundred MB; the catalog reads it once a day and keeps what it needs of it (the
// types with their size and price, the price of a gp3 volume) in a small file in the cache
// directory.
type Catalog struct {
	dir    string // the cache directory; "" for no cache
	http   *http.Client
	base   string
	now    func() time.Time
	notice io.Writer // where it says that it reads a large file; nil for nowhere
}

// NewCatalog returns a catalog that keeps its cache in dir ("" for none) and says on notice that it
// is reading a price list.
func NewCatalog(dir string, notice io.Writer) *Catalog {
	return &Catalog{dir: dir, http: &http.Client{}, base: bulkBase, now: time.Now, notice: notice}
}

// NewCatalogWith returns a catalog over a given price list server and clock (for tests).
func NewCatalogWith(dir string, h *http.Client, base string, now func() time.Time, notice io.Writer) *Catalog {
	return &Catalog{dir: dir, http: h, base: base, now: now, notice: notice}
}

// Describe is an error without the request details, safe to show.
func (*Catalog) Describe(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return "price list: " + ue.Err.Error()
	}
	return strings.TrimSpace(err.Error())
}

// digest is what the catalog keeps of the price list of a region.
type digest struct {
	Fetched time.Time `json:"fetched"`
	// GP3 is the price of a GB of gp3 volume for a month, in US dollars.
	GP3   float64 `json:"gp3"`
	Types []entry `json:"types"`
}

// entry is one instance type with its on-demand price per hour in US dollars.
type entry struct {
	Name   string  `json:"name"`
	VCPU   int     `json:"vcpu"`
	RAMGB  float64 `json:"ram_gb"`
	Arm    bool    `json:"arm,omitempty"`
	Hourly float64 `json:"hourly"`
}

// Offers lists the instance types of the region of the location that have a list price, with the
// price of a gp3 system disk of diskGB (50 GB for 0) added. The prices are in US dollars without VAT.
func (c *Catalog) Offers(ctx context.Context, location string, diskGB int) ([]model.Offer, error) {
	region := Options{Location: location}.region()
	if region == "" {
		return nil, fmt.Errorf("no region: give --location or SERVER_LOCATION")
	}
	d, err := c.digestOf(ctx, region)
	if err != nil {
		return nil, err
	}
	if diskGB <= 0 {
		diskGB = int(Options{}.rootGB())
	}
	disk := float64(diskGB) * d.GP3 // per month
	out := make([]model.Offer, 0, len(d.Types))
	for _, t := range d.Types {
		arch := "x86"
		if t.Arm {
			arch = "arm"
		}
		out = append(out, model.Offer{
			Provider: "aws", Type: t.Name, Location: location, VCPU: t.VCPU, RAMGB: t.RAMGB, Arch: arch, SysDiskGB: diskGB,
			HourlyPrice: t.Hourly + disk/model.HoursPerMonth, MonthlyPrice: t.Hourly*model.HoursPerMonth + disk, Currency: "USD",
		})
	}
	return out, nil
}

// digestOf is the digest of the region: the cached one if it is young enough, else a new one from
// the price list. If the list cannot be read, an old digest is better than none.
func (c *Catalog) digestOf(ctx context.Context, region string) (*digest, error) {
	cached := c.load(region)
	if cached != nil && c.now().Sub(cached.Fetched) < cacheMaxAge {
		return cached, nil
	}
	d, err := c.fetch(ctx, region)
	switch {
	case err == nil:
		c.save(region, d)
		return d, nil
	case cached != nil && ctx.Err() == nil:
		return cached, nil
	}
	return nil, err
}

func (c *Catalog) file(region string) string {
	if c.dir == "" {
		return ""
	}
	return filepath.Join(c.dir, "aws-ec2-"+region+".json")
}

func (c *Catalog) load(region string) *digest {
	f := c.file(region)
	if f == "" {
		return nil
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return nil
	}
	var d digest
	if json.Unmarshal(b, &d) != nil || len(d.Types) == 0 || d.GP3 <= 0 {
		return nil
	}
	return &d
}

// save keeps the digest; a cache that cannot be written only costs the next run a new download.
func (c *Catalog) save(region string, d *digest) {
	f := c.file(region)
	if f == "" {
		return
	}
	b, err := json.Marshal(d)
	if err != nil || os.MkdirAll(c.dir, 0o755) != nil {
		return
	}
	// a file of its own, then rename: two runs at once do not read half a file
	tmp, err := os.CreateTemp(c.dir, "aws-ec2-*.tmp")
	if err != nil {
		return
	}
	_, werr := tmp.Write(b)
	if cerr := tmp.Close(); werr != nil || cerr != nil || os.Rename(tmp.Name(), f) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// fetch reads the price list of the region and digests it.
func (c *Catalog) fetch(ctx context.Context, region string) (*digest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+region+"/index.json", nil)
	if err != nil {
		return nil, err
	}
	if c.notice != nil {
		fmt.Fprintf(c.notice, "aws: reading the price list of %s (a large file, once a day) ...\n", region)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }() // read only
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("no price list for region %s", region)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("price list: HTTP %d", resp.StatusCode)
	}
	d, err := parseBulk(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("price list: %w", err)
	}
	d.Fetched = c.now()
	return d, nil
}

// sku is the part of a product of the price list the catalog reads.
type sku struct {
	ProductFamily string            `json:"productFamily"`
	Attributes    map[string]string `json:"attributes"`
}

// wanted says what a product is to the catalog: an instance type of the shared Linux fleet (name
// is its type), the gp3 volume (name "gp3"), or nothing (name "").
func (s sku) wanted() (name string) {
	a := s.Attributes
	switch {
	case s.ProductFamily == "Compute Instance" && a["operatingSystem"] == "Linux" && a["tenancy"] == "Shared" &&
		a["preInstalledSw"] == "NA" && a["capacitystatus"] == "Used" && a["operation"] == "RunInstances" &&
		(a["licenseModel"] == "" || a["licenseModel"] == "No License required") && (a["locationType"] == "" || a["locationType"] == "AWS Region"):
		return a["instanceType"]
	case s.ProductFamily == "Storage" && a["volumeApiName"] == "gp3":
		return "gp3"
	}
	return ""
}

// bulk is what is read of a price list so far.
type bulk struct {
	wanted map[string]sku // the products the catalog wants, by SKU
	hourly map[string]float64
	gp3    float64
}

// parseBulk digests a price list. The file is read as a stream, as it does not fit in memory in one
// piece: the products come first and are reduced to the wanted ones, the prices after them.
func parseBulk(r io.Reader) (*digest, error) {
	dec := json.NewDecoder(bufio.NewReaderSize(r, 1<<20))
	b := &bulk{wanted: map[string]sku{}, hourly: map[string]float64{}}
	if err := open(dec, '{'); err != nil {
		return nil, err
	}
	for dec.More() {
		key, err := member(dec)
		if err != nil {
			return nil, err
		}
		switch key {
		case "products":
			err = b.products(dec)
		case "terms":
			err = b.terms(dec)
		default:
			err = skip(dec)
		}
		if err != nil {
			return nil, err
		}
	}
	return digestOfPrices(b.wanted, b.hourly, b.gp3)
}

// products keeps the wanted products.
func (b *bulk) products(dec *json.Decoder) error {
	return each(dec, func(id string) error {
		var s sku
		if err := dec.Decode(&s); err != nil {
			return err
		}
		if s.wanted() != "" {
			b.wanted[id] = s
		}
		return nil
	})
}

// terms reads the on-demand prices of the wanted products; the other terms are skipped.
func (b *bulk) terms(dec *json.Decoder) error {
	return each(dec, func(kind string) error {
		if kind != "OnDemand" {
			return skip(dec)
		}
		return each(dec, func(id string) error {
			s, ok := b.wanted[id]
			if !ok {
				return skip(dec)
			}
			return onDemand(dec, s, b.hourly, &b.gp3)
		})
	})
}

// onDemand reads the terms of one SKU and keeps the lowest price per hour (per GB and month for
// the volume).
func onDemand(dec *json.Decoder, s sku, hourly map[string]float64, gp3 *float64) error {
	var terms map[string]struct {
		PriceDimensions map[string]struct {
			Unit         string            `json:"unit"`
			PricePerUnit map[string]string `json:"pricePerUnit"`
		} `json:"priceDimensions"`
	}
	if err := dec.Decode(&terms); err != nil {
		return err
	}
	name, unit := s.wanted(), "Hrs"
	if name == "gp3" {
		unit = "GB-Mo"
	}
	for _, t := range terms {
		for _, d := range t.PriceDimensions {
			p, err := strconv.ParseFloat(d.PricePerUnit["USD"], 64)
			if d.Unit != unit || err != nil || p <= 0 {
				continue
			}
			switch cur, ok := hourly[name]; {
			case name == "gp3":
				*gp3 = p
			case !ok || p < cur:
				hourly[name] = p
			}
		}
	}
	return nil
}

// digestOfPrices joins the sizes of the products with their prices.
func digestOfPrices(wanted map[string]sku, hourly map[string]float64, gp3 float64) (*digest, error) {
	d := &digest{GP3: gp3}
	seen := map[string]bool{}
	for _, s := range wanted {
		name := s.wanted()
		p, ok := hourly[name]
		if name == "gp3" || !ok || seen[name] {
			continue
		}
		vcpu, err := strconv.Atoi(s.Attributes["vcpu"])
		ram := gib(s.Attributes["memory"])
		if err != nil || ram <= 0 {
			continue
		}
		seen[name] = true
		d.Types = append(d.Types, entry{Name: name, VCPU: vcpu, RAMGB: ram, Arm: strings.Contains(s.Attributes["physicalProcessor"], "Graviton"), Hourly: p})
	}
	switch {
	case len(d.Types) == 0:
		return nil, errors.New("no instance types in the price list")
	case gp3 <= 0:
		return nil, errors.New("no price of gp3 volumes in the price list")
	}
	return d, nil
}

// gib reads a memory size such as "16 GiB" or "1,024 GiB".
func gib(s string) float64 {
	v, _, _ := strings.Cut(strings.ReplaceAll(s, ",", ""), " ")
	f, _ := strconv.ParseFloat(v, 64)
	return f
}

// open reads the opening bracket of an object; member the next key; each calls fn for every
// member of an object, with the decoder just after the key; skip drops the next value.
func open(dec *json.Decoder, want json.Delim) error {
	t, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := t.(json.Delim); !ok || d != want {
		return fmt.Errorf("unexpected %v in the price list", t)
	}
	return nil
}

func member(dec *json.Decoder) (string, error) {
	t, err := dec.Token()
	if err != nil {
		return "", err
	}
	k, ok := t.(string)
	if !ok {
		return "", fmt.Errorf("unexpected %v in the price list", t)
	}
	return k, nil
}

func each(dec *json.Decoder, fn func(key string) error) error {
	if err := open(dec, '{'); err != nil {
		return err
	}
	for dec.More() {
		k, err := member(dec)
		if err != nil {
			return err
		}
		if err := fn(k); err != nil {
			return err
		}
	}
	return open(dec, '}')
}

func skip(dec *json.Decoder) error {
	depth := 0
	for {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := t.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
		if depth == 0 {
			return nil
		}
	}
}
