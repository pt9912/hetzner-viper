package hetzner

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/pricecache"
	"github.com/pt9912/hetzner-viper/internal/model"
	"golang.org/x/sync/errgroup"
)

// The pages of the Hetzner website about the server families name each type with its vCPUs, memory
// and disk, and a data file the pages read has the price of every type in every location (in euro and
// dollar, without VAT and without the fee for the IPv4 address). Both are public: no account and no
// token. They are web pages, not an API, so what is read from them is checked (a card that does not
// look as expected is left out), and a change shows as missing prices, not as wrong ones.
const (
	siteBase    = "https://www.hetzner.com/"
	dataFile    = "_resources/app/data/bench/cloud_data.json"
	publicCache = "hetzner-web.json"
)

// familyPages are the pages of the families, by their name in the address.
func familyPages() []string {
	return []string{"cloud/regular-performance/", "cloud/general-purpose/", "cloud/cost-optimized/"}
}

// Public is the price list of the website. It keeps what it reads for a day.
type Public struct {
	http   *http.Client
	base   string
	dir    string // the cache directory; "" for none
	now    func() time.Time
	notice io.Writer
}

// NewPublic returns the catalog of the website; dir is the cache directory ("" for none) and notice is
// where it says that it reads the pages.
func NewPublic(dir string, notice io.Writer) *Public {
	return &Public{http: &http.Client{Timeout: 2 * time.Minute}, base: siteBase, dir: dir, now: time.Now, notice: notice}
}

// NewPublicWith returns a catalog over a given site (for tests).
func NewPublicWith(h *http.Client, base, dir string, now func() time.Time, notice io.Writer) *Public {
	return &Public{http: h, base: base, dir: dir, now: now, notice: notice}
}

// publicDigest is what is kept of the website: each type with its size and its prices in euro per
// location, without VAT.
type publicDigest struct {
	Fetched time.Time    `json:"fetched"`
	Types   []publicType `json:"types"`
	// IPv4 is the fee for a public IPv4 address, in euro without VAT.
	IPv4 publicPrices `json:"ipv4"`
}

type publicType struct {
	Name   string                  `json:"name"` // as SERVER_TYPE takes it: "cpx42"
	VCPU   int                     `json:"vcpu"`
	RAMGB  float64                 `json:"ram_gb"`
	DiskGB int                     `json:"disk_gb"`
	Arch   string                  `json:"arch"`
	Prices map[string]publicPrices `json:"prices"` // by location
}

type publicPrices struct {
	Hourly  float64 `json:"hourly"`
	Monthly float64 `json:"monthly"`
}

// Offers lists the server types in the location ("" for all of them) with their prices in euro
// without VAT, the IPv4 address included. The disk comes with the type. A type the pages mark as not available is left out.
func (p *Public) Offers(ctx context.Context, location string, _ int) ([]model.Offer, error) {
	d, err := p.digest(ctx)
	if err != nil {
		return nil, err
	}
	var out []model.Offer
	for _, t := range d.Types {
		locs := make([]string, 0, len(t.Prices))
		for l := range t.Prices {
			locs = append(locs, l)
		}
		sort.Strings(locs)
		for _, l := range locs {
			if location != "" && l != location {
				continue
			}
			pr := t.Prices[l]
			out = append(out, model.Offer{
				Provider: "hetzner", Type: t.Name, Location: l, VCPU: t.VCPU, RAMGB: t.RAMGB, DiskGB: t.DiskGB, Arch: t.Arch,
				HourlyPrice: pr.Hourly + d.IPv4.Hourly, MonthlyPrice: pr.Monthly + d.IPv4.Monthly, Currency: "EUR", IPv4: true,
			})
		}
	}
	return out, nil
}

// digest is the cached digest if it is young enough, else a new one. If the pages cannot be read, an
// old digest is better than none.
func (p *Public) digest(ctx context.Context) (*publicDigest, error) {
	return pricecache.Get(ctx, p.dir, publicCache, p.now,
		func(d *publicDigest) time.Time { return d.Fetched },
		func(d *publicDigest) bool { return len(d.Types) > 0 && d.IPv4.Monthly > 0 },
		p.fetch)
}

// fetch reads the pages and the data file at the same time.
func (p *Public) fetch(ctx context.Context) (*publicDigest, error) {
	if p.notice != nil {
		fmt.Fprintln(p.notice, "hetzner: reading the public pricing pages (once a day) ...")
	}
	pages := familyPages()
	bodies := make([]string, len(pages)+1)
	g, gc := errgroup.WithContext(ctx)
	for i, page := range pages {
		g.Go(func() (err error) { bodies[i], err = p.get(gc, p.base+page); return err })
	}
	g.Go(func() (err error) { bodies[len(pages)], err = p.get(gc, p.base+dataFile); return err })
	if err := g.Wait(); err != nil {
		return nil, err
	}
	prices, ip, err := dataPrices(bodies[len(pages)])
	if err != nil {
		return nil, err
	}
	d := &publicDigest{Fetched: p.now(), IPv4: ip}
	for i, body := range bodies[:len(pages)] {
		cards := cardsOf(body)
		// a page that gives nothing has changed: a digest without a family would be kept for a day
		if len(cards) == 0 {
			return nil, fmt.Errorf("no server types found on the page %s (did it change?)", pages[i])
		}
		for _, c := range cards {
			if t, ok := c.typeWith(prices); ok {
				d.Types = append(d.Types, t)
			}
		}
	}
	if len(d.Types) == 0 {
		return nil, fmt.Errorf("no prices found on the pricing pages (did they change?)")
	}
	return d, nil
}

func (p *Public) get(ctx context.Context, address string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept-Language", "en")
	req.Header.Set("User-Agent", "hetzner-viper (+https://github.com/pt9912/hetzner-viper)")
	resp, err := p.http.Do(req)
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
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return "", fmt.Errorf("pricing page: %w", err)
	}
	return string(b), nil
}

// card is the card of a server type on a page: the text of its name, vCPUs, memory and disk.
type card struct {
	name      string
	available bool
	vcpu      int
	ramGB     float64
	diskGB    int
}

var (
	tagRe = regexp.MustCompile(`<[^>]+>`)
	// cardRe reads a card in the text of a page: "CCX13 2 AMD 8 GB 80 GB NVMe from /month € 43.49 per month".
	cardRe = regexp.MustCompile(`\b((?:CPX|CCX|CX|CAX)[0-9]{2})((?: New)?(?: not available)?) ([0-9]+) [A-Za-z®/ ]+? ([0-9]+) GB ([0-9]+) GB NVMe from /month € [0-9.]+ per month`)
)

// cardsOf reads the cards of a page from its text.
func cardsOf(page string) []card {
	text := strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(page, " "))), " ")
	var out []card
	for _, m := range cardRe.FindAllStringSubmatch(text, -1) {
		vcpu, _ := strconv.Atoi(m[3])
		ram, _ := strconv.ParseFloat(m[4], 64)
		disk, _ := strconv.Atoi(m[5])
		out = append(out, card{name: strings.ToLower(m[1]), available: !strings.Contains(m[2], "not available"), vcpu: vcpu, ramGB: ram, diskGB: disk})
	}
	return out
}

// typeWith is the server type of a card with its prices; ok is false for a type that is not available
// or that the data file has no prices for.
func (c card) typeWith(prices map[string]map[string]publicPrices) (publicType, bool) {
	pr, ok := prices[c.name]
	if !c.available || !ok || c.vcpu == 0 || c.ramGB == 0 || c.diskGB == 0 {
		return publicType{}, false
	}
	arch := "x86"
	if strings.HasPrefix(c.name, "cax") {
		arch = "arm"
	}
	return publicType{Name: c.name, VCPU: c.vcpu, RAMGB: c.ramGB, DiskGB: c.diskGB, Arch: arch, Prices: pr}, true
}

// dataFileType is a server type in the data file: its prices by location and currency.
type dataFileType struct {
	Locations map[string]map[string]struct {
		Price     string `json:"price"`
		HourPrice string `json:"hourPrice"`
	} `json:"locations"`
}

// dataPrices reads the data file: the prices in euro of each type by location, by lower-case type,
// and the fee for an IPv4 address.
func dataPrices(body string) (map[string]map[string]publicPrices, publicPrices, error) {
	var data struct {
		IP map[string]struct {
			Price     float64 `json:"price"`
			HourPrice float64 `json:"hourPrice"`
		} `json:"IP"`
		Matrix map[string]map[string]dataFileType `json:"Matrix"`
	}
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		return nil, publicPrices{}, fmt.Errorf("pricing data: %w", err)
	}
	ip := publicPrices{Hourly: data.IP["EUR"].HourPrice, Monthly: data.IP["EUR"].Price}
	if ip.Monthly <= 0 || ip.Hourly <= 0 {
		return nil, publicPrices{}, fmt.Errorf("pricing data: no price of an IPv4 address in the file (did it change?)")
	}
	out := map[string]map[string]publicPrices{}
	for _, types := range data.Matrix {
		for name, t := range types {
			if byLoc := t.euroPrices(); len(byLoc) > 0 {
				out[strings.ToLower(name)] = byLoc
			}
		}
	}
	if len(out) == 0 {
		return nil, publicPrices{}, fmt.Errorf("pricing data: no prices in the file (did it change?)")
	}
	return out, ip, nil
}

// euroPrices are the prices of a type in euro by location name.
func (t dataFileType) euroPrices() map[string]publicPrices {
	byLoc := map[string]publicPrices{}
	for code, cur := range t.Locations {
		monthly, err1 := strconv.ParseFloat(cur["EUR"].Price, 64)
		hourly, err2 := strconv.ParseFloat(cur["EUR"].HourPrice, 64)
		if err1 == nil && err2 == nil && monthly > 0 {
			byLoc[locationName(code)] = publicPrices{Hourly: hourly, Monthly: monthly}
		}
	}
	// Falkenstein and Nuremberg are both Germany and cost the same; the file lists only one
	if _, ok := byLoc["fsn1"]; !ok {
		if nbg, ok := byLoc["nbg1"]; ok {
			byLoc["fsn1"] = nbg
		}
	}
	return byLoc
}

// locationName is the name of a location as the API and SERVER_LOCATION have it: the file says
// "HIL1" and "ASH1" where the name is "hil" and "ash".
func locationName(code string) string {
	l := strings.ToLower(code)
	if l == "hil1" || l == "ash1" {
		return strings.TrimSuffix(l, "1")
	}
	return l
}
