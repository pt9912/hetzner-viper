package aws

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/pricecache"
	"github.com/pt9912/hetzner-viper/internal/model"
)

// bulkBase is where AWS publishes the price lists of EC2, one file per region. They are public:
// they need no account and no login.
const bulkBase = "https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonEC2/current/"

// vpcBase is where the price list of the network service is, which has the fee for a public IPv4
// address; it is small.
const vpcBase = "https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonVPC/current/"

// Catalog is the price list of the on-demand Linux instances of EC2. The list of a region is a
// file of several hundred MB; the catalog reads it once a day and keeps what it needs of it (the
// types with their size and price, the price of a gp3 volume) in a small file in the cache
// directory.
type Catalog struct {
	dir    string // the cache directory; "" for no cache
	http   *http.Client
	base   string
	vpc    string // the price list of the network service
	now    func() time.Time
	notice io.Writer // where it says that it reads a large file; nil for nowhere
	// retryWait is the pause before the second try of a download that broke off, and twice that
	// before the third.
	retryWait time.Duration
}

// tries is how often a download that breaks off is started.
const tries = 3

// NewCatalog returns a catalog that keeps its cache in dir ("" for none) and says on notice that it
// is reading a price list.
func NewCatalog(dir string, notice io.Writer) *Catalog {
	return &Catalog{dir: dir, http: &http.Client{Timeout: 15 * time.Minute}, base: bulkBase, vpc: vpcBase, now: time.Now, notice: notice, retryWait: 2 * time.Second}
}

// NewCatalogWith returns a catalog over a given price list server and clock (for tests).
func NewCatalogWith(dir string, h *http.Client, base string, now func() time.Time, notice io.Writer) *Catalog {
	// the fee of the IPv4 address is read from <base>vpc/ in the tests
	return &Catalog{dir: dir, http: h, base: base, vpc: base + "vpc/", now: now, notice: notice, retryWait: time.Millisecond}
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
	// GP3 is the price of a GB of gp3 volume for a month, in US dollars; IPv4 the fee for a public
	// IPv4 address per hour.
	GP3   float64 `json:"gp3"`
	IPv4  float64 `json:"ipv4"`
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

// validRegion is what a region looks like; it is also a part of the name of the cache file.
var validRegion = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Offers lists the instance types of the region of the location that have a list price, with the
// price of a gp3 system disk of diskGB (50 GB for 0) added. The prices are in US dollars without VAT.
func (c *Catalog) Offers(ctx context.Context, location string, diskGB int) ([]model.Offer, error) {
	region := Options{Location: location}.region()
	if !validRegion.MatchString(region) {
		return nil, fmt.Errorf("no region: give --location or SERVER_LOCATION (eu-central-1)")
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
			HourlyPrice: t.Hourly + d.IPv4 + disk/model.HoursPerMonth, MonthlyPrice: (t.Hourly+d.IPv4)*model.HoursPerMonth + disk, Currency: "USD", IPv4: true,
		})
	}
	return out, nil
}

func cacheName(region string) string { return "aws-ec2-" + region + ".json" }

// digestOf is the digest of the region: the kept one if it is young enough, else a new one from the
// price list. If the list cannot be read, an old digest is better than none.
func (c *Catalog) digestOf(ctx context.Context, region string) (*digest, error) {
	return pricecache.Get(ctx, c.dir, cacheName(region), c.now,
		func(d *digest) time.Time { return d.Fetched },
		func(d *digest) bool { return len(d.Types) > 0 && d.GP3 > 0 && d.IPv4 > 0 },
		func(ctx context.Context) (*digest, error) { return c.fetch(ctx, region) })
}

// fetch reads the price list of the region and digests it. A download that breaks off (the file is
// large, a connection can fail on the way) is started again, up to tries times.
func (c *Catalog) fetch(ctx context.Context, region string) (*digest, error) {
	if c.notice != nil {
		fmt.Fprintf(c.notice, "aws: reading the price list of %s (a large file, once a day) ...\n", region)
	}
	// the small list first: if it fails, the large download has not been made for nothing
	ip, err := c.ipv4(ctx, region)
	if err != nil {
		return nil, err
	}
	for try := 1; try <= tries; try++ {
		var d *digest
		if d, err = c.download(ctx, region); err == nil {
			d.IPv4, d.Fetched = ip, c.now()
			return d, nil
		}
		if !brokeOff(err) || try == tries {
			break
		}
		if c.notice != nil {
			fmt.Fprintf(c.notice, "aws: the download of the price list broke off (%s), trying again (%d/%d)\n", c.Describe(err), try+1, tries)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(try) * c.retryWait):
		}
	}
	if brokeOff(err) {
		return nil, fmt.Errorf("the download of the price list broke off %d times (%s): check the network, a proxy or VPN may cut long downloads", tries, c.Describe(err))
	}
	return nil, err
}

// brokeOff reports whether an error is a connection that was reset, timed out or ended too early,
// which another try may get past. A wrong answer, a wrong list, a name that does not resolve (the
// network is down) or a cancelled run is not.
func brokeOff(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var ne net.Error
	return errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET) || (errors.As(err, &ne) && ne.Timeout())
}

// download makes one try.
func (c *Catalog) download(ctx context.Context, region string) (*digest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+region+"/index.json", nil)
	if err != nil {
		return nil, err
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

// ipv4 is the fee per hour in US dollars for a public IPv4 address in use, from the price list of the
// network service of the region.
func (c *Catalog) ipv4(ctx context.Context, region string) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.vpc+region+"/index.json", nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("price list of the network: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // read only
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound:
		return 0, fmt.Errorf("no price list for region %s", region)
	case resp.StatusCode != http.StatusOK:
		return 0, fmt.Errorf("price list of the network: HTTP %d", resp.StatusCode)
	}
	var list struct {
		Products map[string]struct {
			Attributes struct {
				Usagetype string `json:"usagetype"`
			} `json:"attributes"`
		} `json:"products"`
		Terms struct {
			OnDemand map[string]map[string]struct {
				PriceDimensions map[string]struct {
					Unit         string            `json:"unit"`
					PricePerUnit map[string]string `json:"pricePerUnit"`
				} `json:"priceDimensions"`
			} `json:"OnDemand"`
		} `json:"terms"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&list); err != nil {
		return 0, fmt.Errorf("price list of the network: %w", err)
	}
	for sku, p := range list.Products {
		// "EUC1-PublicIPv4:InUseAddress": the usage of an address that is attached to something
		if !strings.HasSuffix(p.Attributes.Usagetype, "PublicIPv4:InUseAddress") {
			continue
		}
		for _, term := range list.Terms.OnDemand[sku] {
			for _, d := range term.PriceDimensions {
				if v, err := strconv.ParseFloat(d.PricePerUnit["USD"], 64); err == nil && d.Unit == "Hrs" && v > 0 {
					return v, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("price list of the network: no fee for a public IPv4 address found (did it change?)")
}
