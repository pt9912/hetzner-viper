// Package ecb is the adapter of the port fx (internal/port/driven/fx) over the daily reference rates
// of the European Central Bank: a small public file, no account, no key.
package ecb

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/pricecache"
	"github.com/pt9912/hetzner-viper/internal/port/driven/fx"
)

const (
	daily       = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml"
	cacheName   = "ecb-rates.json"
	cacheMaxAge = 24 * time.Hour
	// staleMaxAge is how old rates may be if the file cannot be read: the ECB publishes on working
	// days, so a week is a long break; older rates would give a ranking on a wrong basis.
	staleMaxAge = 7 * 24 * time.Hour
	maxBody     = 1 << 20
)

// Client reads the rates, at most once a day.
type Client struct {
	url  string
	http *http.Client
	dir  string // the cache directory; "" for none
	now  func() time.Time
}

// New returns a client that keeps the rates in dir ("" for none).
func New(dir string) *Client {
	return &Client{url: daily, http: &http.Client{Timeout: 30 * time.Second}, dir: dir, now: time.Now}
}

// NewWith returns a client over a given file address and clock (for tests).
func NewWith(address string, h *http.Client, dir string, now func() time.Time) *Client {
	return &Client{url: address, http: h, dir: dir, now: now}
}

// table is the rates of a day: how much of each currency one euro is.
type table struct {
	Fetched time.Time          `json:"fetched"`
	Date    string             `json:"date"`
	PerEUR  map[string]float64 `json:"per_eur"`
}

// Rate is the rate from one currency to another, over the euro.
func (c *Client) Rate(ctx context.Context, from, to string) (fx.Rate, error) {
	t, err := c.table(ctx)
	if err != nil {
		return fx.Rate{}, err
	}
	perFrom, okFrom := perEUR(t, from)
	perTo, okTo := perEUR(t, to)
	if !okFrom || !okTo {
		return fx.Rate{}, fmt.Errorf("no exchange rate from %s to %s", from, to)
	}
	return fx.Rate{Value: perTo / perFrom, Date: t.Date}, nil
}

func perEUR(t *table, currency string) (float64, bool) {
	if currency == "EUR" {
		return 1, true
	}
	v, ok := t.PerEUR[currency]
	return v, ok && v > 0
}

// table is the cached rates if they are of the last day, else new ones; if the file cannot be read,
// old ones are better than none.
func (c *Client) table(ctx context.Context) (*table, error) {
	var cached table
	have := pricecache.Read(c.dir, cacheName, &cached) && len(cached.PerEUR) > 0
	if have && c.now().Sub(cached.Fetched) < cacheMaxAge {
		return &cached, nil
	}
	fresh, err := c.fetch(ctx)
	switch {
	case err == nil:
		pricecache.Write(c.dir, cacheName, fresh)
		return fresh, nil
	case have && ctx.Err() == nil && c.now().Sub(cached.Fetched) < staleMaxAge:
		return &cached, nil
	}
	return nil, err
}

// envelope is the part of the file that is read: the day and the rate of each currency.
type envelope struct {
	Cube struct {
		Day struct {
			Time  string `xml:"time,attr"`
			Rates []struct {
				Currency string `xml:"currency,attr"`
				Rate     string `xml:"rate,attr"`
			} `xml:"Cube"`
		} `xml:"Cube"`
	} `xml:"Cube"`
}

func (c *Client) fetch(ctx context.Context) (*table, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ue, ok := err.(*url.Error); ok {
			err = ue.Err
		}
		return nil, fmt.Errorf("exchange rates: %w", err)
	}
	defer func() { _ = resp.Body.Close() }() // read only
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("exchange rates: HTTP %d", resp.StatusCode)
	}
	var e envelope
	if err := xml.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&e); err != nil {
		return nil, fmt.Errorf("exchange rates: %w", err)
	}
	t := &table{Fetched: c.now(), Date: e.Cube.Day.Time, PerEUR: map[string]float64{}}
	for _, r := range e.Cube.Day.Rates {
		if v, err := strconv.ParseFloat(r.Rate, 64); err == nil && v > 0 {
			t.PerEUR[r.Currency] = v
		}
	}
	if len(t.PerEUR) == 0 {
		return nil, fmt.Errorf("exchange rates: none in the file")
	}
	return t, nil
}
