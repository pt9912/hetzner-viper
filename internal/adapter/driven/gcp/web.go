package gcp

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/pricecache"
	"github.com/pt9912/hetzner-viper/internal/model"
	"golang.org/x/sync/errgroup"
)

// The public pricing pages of Google Cloud list the machine types of a family with their cores,
// memory and price for every region, and the disks with their price per GiB. They need no login and
// no project. They are web pages, not an API: the tables are in the data the page loads, so what is
// read from them is checked (a row that does not look as expected is left out) and a change in the
// pages shows as missing prices, not as wrong ones.
const (
	pagesBase = "https://cloud.google.com/products/compute/pricing/"
	disksPage = "https://cloud.google.com/compute/disks-image-pricing"
	// networkPage has the fee for an external IPv4 address, which is the same in every region.
	networkPage = "https://cloud.google.com/vpc/network-pricing"
	ipv4Cache   = "gcp-ipv4.json"
	webCache    = "gcp-web.json"
)

// pricePages are the pages of the machine type families, by their name in the address.
func pricePages() []string {
	return []string{"general-purpose", "compute-optimized", "memory-optimized", "storage-optimized"}
}

// webDigest is what the catalog keeps of the pages: for each region the machine types with their
// price per hour in US dollars, and the price per GiB and month of the boot disk types.
type webDigest struct {
	Fetched  time.Time                     `json:"fetched"`
	Machines map[string][]webMachine       `json:"machines"`
	Disks    map[string]map[string]float64 `json:"disks"`
	// IPv4 is the fee for an external IPv4 address in use on a standard VM per hour, in US dollars.
	IPv4 float64 `json:"ipv4"`
}

type webMachine struct {
	Name   string  `json:"name"`
	VCPU   int     `json:"vcpu"`
	RAMGB  float64 `json:"ram_gb"`
	Hourly float64 `json:"hourly"`
}

// web reads the pages once a day.
type web struct {
	http    *http.Client
	base    string   // where the pages of the families are, pagesBase unless a test says otherwise
	disks   string   // the page of the disks
	network string   // the page of the network prices
	pages   []string // the pages of the families; pricePages() unless a test says otherwise
	dir     string   // the cache directory; "" for none
	now     func() time.Time
	notice  io.Writer
}

// offers are the machine types of the zone's region with their price, a disk of diskGB added.
func (w *web) offers(ctx context.Context, zone, region string, diskGB int) ([]model.Offer, error) {
	d, err := w.digest(ctx)
	if err != nil {
		return nil, err
	}
	machines, disks := d.Machines[region], d.Disks[region]
	if len(machines) == 0 {
		return nil, fmt.Errorf("no prices for region %s", region)
	}
	if diskGB <= 0 {
		diskGB = int(Options{}.diskGB())
	}
	out := make([]model.Offer, 0, len(machines))
	for _, m := range machines {
		perGB, ok := disks[diskType(m.Name)]
		if !ok || strings.Contains(m.Name, "lssd") {
			continue
		}
		disk := float64(diskGB) * perGB // per month
		arch := "x86"
		if armFamily(m.Name) {
			arch = "arm"
		}
		out = append(out, model.Offer{
			Provider: "gcp", Type: m.Name, Location: zone, VCPU: m.VCPU, RAMGB: m.RAMGB, Arch: arch, SysDiskGB: diskGB,
			HourlyPrice: m.Hourly + d.IPv4 + disk/model.HoursPerMonth, MonthlyPrice: (m.Hourly+d.IPv4)*model.HoursPerMonth + disk, Currency: "USD", IPv4: true,
		})
	}
	return out, nil
}

// sharedCore tells the machine types with a shared core: they run on a fraction of the cores the pages
// say and are left out, as with the project.
func sharedCore(name string) bool {
	switch name {
	case "e2-micro", "e2-small", "e2-medium", "f1-micro", "g1-small":
		return true
	}
	return false
}

// gpuFamily tells the families whose machine types come with a GPU, which is billed apart.
func gpuFamily(name string) bool {
	for _, f := range []string{"a2-", "a3-", "a4-", "a4x-", "g2-", "g4-"} {
		if strings.HasPrefix(name, f) {
			return true
		}
	}
	return false
}

// armFamily tells the families with an Arm processor.
func armFamily(name string) bool {
	for _, f := range []string{"t2a-", "c4a-", "n4a-"} {
		if strings.HasPrefix(name, f) {
			return true
		}
	}
	return false
}

// digest is the digest of the pages: the cached one if it is young enough, else a new one. If the
// pages cannot be read, an old digest is better than none.
func (w *web) digest(ctx context.Context) (*webDigest, error) {
	return pricecache.Get(ctx, w.dir, webCache, w.now,
		func(d *webDigest) time.Time { return d.Fetched },
		func(d *webDigest) bool { return len(d.Machines) > 0 && len(d.Disks) > 0 && d.IPv4 > 0 },
		w.fetch)
}

// fetch reads all the pages, at the same time.
func (w *web) fetch(ctx context.Context) (*webDigest, error) {
	if w.notice != nil {
		fmt.Fprintln(w.notice, "gcp: reading the public pricing pages (large files, once a day) ...")
	}
	pages := w.pages
	if pages == nil {
		pages = pricePages()
	}
	bodies := make([]string, len(pages)+1)
	var ip float64
	g, gc := errgroup.WithContext(ctx)
	g.Go(func() (err error) { ip, err = w.ipv4(gc); return err })
	for i, p := range pages {
		g.Go(func() (err error) { bodies[i], err = w.get(gc, w.base+p+"?hl=en"); return err })
	}
	g.Go(func() (err error) { bodies[len(pages)], err = w.get(gc, w.disks+"?hl=en"); return err })
	if err := g.Wait(); err != nil {
		return nil, err
	}
	d := &webDigest{Fetched: w.now(), IPv4: ip, Machines: map[string][]webMachine{}, Disks: map[string]map[string]float64{}}
	for i, body := range bodies[:len(pages)] {
		found := 0
		for _, t := range tablesOf(body) {
			ms := machinesOf(t.cells, d.Machines[t.region])
			d.Machines[t.region] = append(d.Machines[t.region], ms...)
			found += len(ms)
		}
		// a page that gives nothing has changed: a digest without a family would be kept for a day
		if found == 0 {
			return nil, fmt.Errorf("no prices found on the pricing page %s (did it change?)", pages[i])
		}
	}
	for _, t := range tablesOf(bodies[len(pages)]) {
		for name, price := range disksOf(t.cells) {
			if d.Disks[t.region] == nil {
				d.Disks[t.region] = map[string]float64{}
			}
			d.Disks[t.region][name] = price
		}
	}
	if len(d.Machines) == 0 || len(d.Disks) == 0 {
		return nil, fmt.Errorf("no prices found on the pricing pages (did they change?)")
	}
	return d, nil
}

func (w *web) get(ctx context.Context, address string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept-Language", "en")
	// the pages answer the default name of Go's HTTP client with nothing: say who is asking
	req.Header.Set("User-Agent", "hetzner-viper (+https://github.com/pt9912/hetzner-viper)")
	resp, err := w.http.Do(req)
	if err != nil {
		if ue, ok := err.(*url.Error); ok {
			err = ue.Err
		}
		return "", fmt.Errorf("pricing page: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // read only
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("pricing page: HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("pricing page: %w", err)
	}
	return string(b), nil
}

// A table in the data of a page is followed by the label of its region ("Osaka (asia-northeast2)").
// What lies between two labels are the cells of the table of the second one.
type table struct {
	region string
	cells  []cell
}

// cell is a text cell of a table ("e2-standard-4", "16 GiB") or a price ("$0.13 / 1 hour").
type cell struct {
	text  string
	price float64
	unit  string // of a price: "hour", "month", "gibibyte month", ...; "" for a text
}

// tokenRe finds, in this order of the alternatives, the labels of the regions, the text cells and the
// prices, in the data of a page. The text cells are written with a backslash and u003c for <.
var tokenRe = regexp.MustCompile(`"[^"\\]* \(([a-z]+-[a-z]+[0-9]+)\)"|"\\u003cp\\u003e([^"\\]*)\\u003c/p\\u003e"|"\$([0-9][0-9.,]*) / 1 ([a-z ]+)"`)

// tablesOf cuts the tables out of the data of a page.
func tablesOf(page string) []table {
	var out []table
	var cells []cell
	for _, m := range tokenRe.FindAllStringSubmatch(page, -1) {
		switch {
		case m[1] != "": // the label of a region: the cells since the last one are its table
			if len(cells) > 0 {
				out = append(out, table{region: m[1], cells: cells})
			}
			cells = nil
		case m[2] != "":
			cells = append(cells, cell{text: strings.TrimSpace(m[2])})
		default:
			if v, err := strconv.ParseFloat(strings.ReplaceAll(m[3], ",", ""), 64); err == nil {
				cells = append(cells, cell{price: v, unit: m[4]})
			}
		}
	}
	return out
}

var (
	machineName = regexp.MustCompile(`^[a-z][a-z0-9]*-[a-z0-9-]+$`)
	wholeNumber = regexp.MustCompile(`^[0-9]+$`)
	gib         = regexp.MustCompile(`^([0-9][0-9,.]*) GiB$`)
)

// machinesOf reads the rows of a table of machine types: the name, the virtual CPUs, the memory,
// maybe a local SSD, then the prices per hour. The tables with discounts have more prices in a row
// (committed use, spot) and all of them are lower than the on-demand price, so that is the highest.
// A table with prices per month, and the machine types that are already known (the same table
// comes again with discounts), are left out.
func machinesOf(cells []cell, known []webMachine) []webMachine {
	have := map[string]bool{}
	for _, k := range known {
		have[k.Name] = true
	}
	var out []webMachine
	for i := 0; i+3 < len(cells); i++ {
		c := cells[i]
		if c.unit != "" || !machineName.MatchString(c.text) || !wholeNumber.MatchString(cells[i+1].text) || gib.FindStringSubmatch(cells[i+2].text) == nil {
			continue
		}
		vcpu, _ := strconv.Atoi(cells[i+1].text)
		ram, _ := strconv.ParseFloat(strings.ReplaceAll(gib.FindStringSubmatch(cells[i+2].text)[1], ",", ""), 64)
		price, unit := rowPrice(cells[i+3:])
		if unit == "hour" && !have[c.text] && vcpu > 0 && ram > 0 && !gpuFamily(c.text) && !sharedCore(c.text) {
			out = append(out, webMachine{Name: c.text, VCPU: vcpu, RAMGB: ram, Hourly: price})
			have[c.text] = true
		}
	}
	return out
}

// rowPrice is the highest price of the cells up to the next row (a text cell that is not the local
// SSD) and the unit of the prices.
func rowPrice(cells []cell) (price float64, unit string) {
	for k, c := range cells {
		switch {
		case c.unit != "":
			if unit == "" {
				unit = c.unit
			}
			if c.unit == unit && c.price > price {
				price = c.price
			}
		case k > 0 && machineName.MatchString(c.text):
			return price, unit
		}
	}
	return price, unit
}

// disksOf reads the price per GiB and month of the boot disk types from a table of disks.
func disksOf(cells []cell) map[string]float64 {
	out := map[string]float64{}
	for i := 0; i+1 < len(cells); i++ {
		if cells[i].unit != "" || cells[i+1].unit != "gibibyte month" {
			continue
		}
		switch cells[i].text {
		case "Balanced provisioned space":
			out["pd-balanced"] = cells[i+1].price
		case "Hyperdisk Balanced provisioned space":
			out["hyperdisk-balanced"] = cells[i+1].price
		}
	}
	return out
}

// ipv4Digest is the fee for an external IPv4 address, kept for a day.
type ipv4Digest struct {
	Fetched time.Time `json:"fetched"`
	Hourly  float64   `json:"hourly"`
}

// ipv4Re finds, in the text of the network page, the row of the addresses in use on standard VMs.
var ipv4Re = regexp.MustCompile(`in use on standard VM instances \$([0-9.]+) / 1 hour`)

// tagRe finds the tags of a page.
var tagRe = regexp.MustCompile(`<[^>]+>`)

// ipv4 is the fee per hour in US dollars for an external IPv4 address in use on a standard VM. It is
// the same in every region.
func (w *web) ipv4(ctx context.Context) (float64, error) {
	d, err := pricecache.Get(ctx, w.dir, ipv4Cache, w.now,
		func(d *ipv4Digest) time.Time { return d.Fetched },
		func(d *ipv4Digest) bool { return d.Hourly > 0 },
		func(ctx context.Context) (*ipv4Digest, error) {
			price, err := w.readIPv4(ctx)
			if err != nil {
				return nil, err
			}
			return &ipv4Digest{Fetched: w.now(), Hourly: price}, nil
		})
	if err != nil {
		return 0, err
	}
	return d.Hourly, nil
}

func (w *web) readIPv4(ctx context.Context) (float64, error) {
	body, err := w.get(ctx, w.network+"?hl=en")
	if err != nil {
		return 0, err
	}
	// the page has a no-break space in the row ("in use on&nbsp; standard VM"): Fields splits on all
	// the spaces of Unicode, where \s in a pattern knows only those of ASCII
	text := strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(body, " "))), " ")
	m := ipv4Re.FindStringSubmatch(text)
	if m == nil {
		return 0, fmt.Errorf("no fee for an external IPv4 address found on the network pricing page (did it change?)")
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("no fee for an external IPv4 address found on the network pricing page (did it change?)")
	}
	return v, nil
}
