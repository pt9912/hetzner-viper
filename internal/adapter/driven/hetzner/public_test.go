package hetzner_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/hetzner"
	"github.com/pt9912/hetzner-viper/internal/model"
)

// card is the card of a server type as the pages show it; the text of the page is what is read.
func card(name, flag, vcpu, cpu, ram, disk, monthly string) string {
	return `<div class="card"><h3>` + name + `</h3><span>` + flag + `</span><div>` + vcpu + `</div><div>` + cpu + `</div><div>` + ram + ` GB</div><div>` +
		disk + `&nbsp;GB NVMe</div><p>from /month</p><p>€ ` + monthly + ` per month, excl. VAT</p></div>`
}

// dataFile is the data the pages read: the prices without the fee for the IPv4 address.
const dataFile = `{"Matrix":{
 "Shared - Regular Performance":{
  "CPX42":{"cores":8,"locations":{
    "NBG1":{"EUR":{"price":"69.49","hourPrice":"0.111400"},"USD":{"price":"80.00","hourPrice":"0.13"}},
    "HEL1":{"EUR":{"price":"69.49","hourPrice":"0.111400"}},
    "SIN":{"EUR":{"price":"93.49","hourPrice":"0.150000"}}}},
  "CPX11":{"cores":2,"locations":{"ASH1":{"EUR":{"price":"17.49","hourPrice":"0.028000"}}}}},
 "Dedicated - General Purpose":{
  "CCX13":{"cores":2,"locations":{"FSN1":{"EUR":{"price":"43.00","hourPrice":"0.069000"}}}}}},
 "IP":{"EUR":{"price":0.5,"hourPrice":0.0008},"USD":{"price":0.6,"hourPrice":0.001}}}`

// site serves the pages of the families and the data file; it counts the requests and answers the
// default name of Go's HTTP client with nothing, as the website's pages may.
type site struct {
	srv      *httptest.Server
	requests atomic.Int32
	status   int
}

func newSite(t *testing.T) *site {
	t.Helper()
	s := &site{status: 200}
	regular := `<html>` + card("CPX42", "", "8", "AMD", "16", "320", "69.99") + card("CPX11", "", "2", "AMD", "2", "40", "17.99") +
		card("CPX99", "", "4", "AMD", "8", "160", "1.00") + `</html>` // a type the data file has no prices for
	general := `<html>` + card("CCX13", "", "2", "AMD", "8", "80", "43.49") + `</html>`
	cost := `<html>` + card("CX23", " not available", "2", "Intel®/AMD", "4", "40", "5.99") + `</html>`
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		if strings.HasPrefix(r.UserAgent(), "Go-http-client") {
			return
		}
		if s.status != 200 {
			w.WriteHeader(s.status)
			return
		}
		switch r.URL.Path {
		case "/cloud/regular-performance/":
			_, _ = w.Write([]byte(regular))
		case "/cloud/general-purpose/":
			_, _ = w.Write([]byte(general))
		case "/cloud/cost-optimized/":
			_, _ = w.Write([]byte(cost))
		case "/_resources/app/data/bench/cloud_data.json":
			_, _ = w.Write([]byte(dataFile))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *site) public(dir string, now func() time.Time, notice *bytes.Buffer) *hetzner.Public {
	if notice == nil {
		return hetzner.NewPublicWith(s.srv.Client(), s.srv.URL+"/", dir, now, nil)
	}
	return hetzner.NewPublicWith(s.srv.Client(), s.srv.URL+"/", dir, now, notice)
}

func TestThePublicPagesGiveTheTypesWithTheirPricesAndTheAddress(t *testing.T) {
	s := newSite(t)
	offers, err := s.public("", time.Now, nil).Offers(context.Background(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]model.Offer{}
	for _, o := range offers {
		by[o.Type+"@"+o.Location] = o
	}
	// not available (cx23), without prices in the data (cpx99): left out; fsn1 is nbg1's price
	for _, key := range []string{"cpx42@nbg1", "cpx42@hel1", "cpx42@fsn1", "cpx42@sin", "cpx11@ash", "ccx13@fsn1"} {
		if _, ok := by[key]; !ok {
			t.Errorf("missing %s: %v", key, keys(by))
		}
	}
	for key := range by {
		if strings.HasPrefix(key, "cx23") || strings.HasPrefix(key, "cpx99") {
			t.Errorf("must be left out: %s", key)
		}
	}
	o := by["cpx42@fsn1"]
	// 69.49 and the address at 0.50 a month (= the price on the page, 69.99); 0.1114 and 0.0008 an hour
	if o.Provider != "hetzner" || o.VCPU != 8 || o.RAMGB != 16 || o.DiskGB != 320 || o.Arch != "x86" || o.Currency != "EUR" || o.VATPercent != 0 || !o.IPv4 ||
		abs(o.MonthlyPrice-69.99) > 1e-9 || abs(o.HourlyPrice-0.1122) > 1e-9 {
		t.Errorf("%+v", o)
	}
	if sin := by["cpx42@sin"]; abs(sin.MonthlyPrice-93.99) > 1e-9 {
		t.Errorf("%+v", sin)
	}
	// one location only
	one, err := s.public("", time.Now, nil).Offers(context.Background(), "hel1", 0)
	if err != nil || len(one) != 1 || one[0].Location != "hel1" {
		t.Errorf("%+v %v", one, err)
	}
}

func keys(m map[string]model.Offer) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestThePublicPagesAreReadOnceADay(t *testing.T) {
	s := newSite(t)
	dir := t.TempDir()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	var notice bytes.Buffer
	ask := func(c *hetzner.Public) error { _, err := c.Offers(context.Background(), "", 0); return err }
	if err := ask(s.public(dir, clock, &notice)); err != nil || s.requests.Load() != 4 { // three pages and the data file
		t.Fatalf("first: %v %d", err, s.requests.Load())
	}
	now = now.Add(23 * time.Hour)
	if err := ask(s.public(dir, clock, nil)); err != nil || s.requests.Load() != 4 {
		t.Errorf("within a day: %v %d requests", err, s.requests.Load())
	}
	if strings.Count(notice.String(), "reading the public pricing pages") != 1 {
		t.Errorf("%q", notice.String())
	}
	now = now.Add(2 * time.Hour)
	if err := ask(s.public(dir, clock, nil)); err != nil || s.requests.Load() != 8 {
		t.Errorf("after a day: %v %d requests", err, s.requests.Load())
	}
	// the pages cannot be read any more: the old digest is better than none
	s.status = 503
	now = now.Add(72 * time.Hour)
	if err := ask(s.public(dir, clock, nil)); err != nil {
		t.Errorf("old digest: %v", err)
	}
}

func TestAChangedPageIsAnErrorAndNotKept(t *testing.T) {
	s := newSite(t)
	s.status = 503
	if _, err := s.public("", time.Now, nil).Offers(context.Background(), "", 0); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Errorf("%v", err)
	}
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".json") {
			_, _ = w.Write([]byte(dataFile))
			return
		}
		_, _ = w.Write([]byte("<html>changed</html>"))
	}))
	defer empty.Close()
	dir := t.TempDir()
	_, err := hetzner.NewPublicWith(empty.Client(), empty.URL+"/", dir, time.Now, nil).Offers(context.Background(), "", 0)
	if err == nil || !strings.Contains(err.Error(), "did it change") {
		t.Errorf("%v", err)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "*")); len(files) != 0 {
		t.Errorf("kept: %v", files)
	}
	// a data file without the fee for the address is as good as none
	noIP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".json") {
			_, _ = w.Write([]byte(`{"Matrix":{}}`))
		}
	}))
	defer noIP.Close()
	if _, err := hetzner.NewPublicWith(noIP.Client(), noIP.URL+"/", "", time.Now, nil).Offers(context.Background(), "", 0); err == nil || !strings.Contains(err.Error(), "IPv4") {
		t.Errorf("%v", err)
	}
}

func TestTheProjectIsAskedFirstAndThePublicPagesAreTheFallback(t *testing.T) {
	s := newSite(t)
	var notice bytes.Buffer
	good := hetzner.New("good", api(t).URL)
	// with a token that works: the prices of the project, with its VAT
	c := hetzner.NewCatalogWith(good, s.public("", time.Now, &notice), &notice)
	offers, err := c.Offers(context.Background(), "", 0)
	if err != nil || len(offers) != 1 || offers[0].VATPercent != 19 || notice.Len() != 0 {
		t.Fatalf("%+v %v %q", offers, err, notice.String())
	}
	// with a token that does not: the pages, and the notice says why
	bad := hetzner.NewCatalogWith(hetzner.New("bad", api(t).URL), s.public("", time.Now, &notice), &notice)
	offers, err = bad.Offers(context.Background(), "", 0)
	if err != nil || len(offers) == 0 || offers[0].VATPercent != 0 || !strings.Contains(notice.String(), "the project cannot be read (unauthorized (check HCLOUD_TOKEN)); using the public pricing pages, whose prices are without VAT") {
		t.Errorf("%d offers, %v, %q", len(offers), err, notice.String())
	}
	// without a token: the pages, without a notice about the project
	notice.Reset()
	none := hetzner.NewCatalogWith(nil, s.public("", time.Now, &notice), &notice)
	if offers, err := none.Offers(context.Background(), "", 0); err != nil || len(offers) == 0 || strings.Contains(notice.String(), "cannot be read") {
		t.Errorf("%d offers, %v, %q", len(offers), err, notice.String())
	}
}
