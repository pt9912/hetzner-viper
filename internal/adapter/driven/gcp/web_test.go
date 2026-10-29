package gcp_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/gcp"
	"github.com/pt9912/hetzner-viper/internal/model"
	"google.golang.org/api/cloudbilling/v1"
	"google.golang.org/api/compute/v1"
)

// The pages carry their tables in data in which the text cells are written with a backslash and
// u003c for <.
func text(s string) string {
	bs := string(rune(92))
	return `"` + bs + `u003cp` + bs + `u003e` + s + bs + `u003c/p` + bs + `u003e"`
}

func hourly(v string) string     { return `"$` + v + ` / 1 hour"` }
func monthly(v string) string    { return `"$` + v + ` / 1 month"` }
func label(region string) string { return `"Somewhere (` + region + `)"` }

// regionTables are the four tables of a region in a family's page: per hour and per month, each
// with and without the discount columns; every table is followed by the label of the region.
func regionTables(region string, rows [][]string) string {
	var sb strings.Builder
	for _, kind := range []string{"hour", "month", "hour-discounts", "month-discounts"} {
		sb.WriteString(`[[` + text("Machine type") + "," + text("Virtual CPUs") + `],[`)
		for _, r := range rows {
			for _, cell := range r[:len(r)-1] {
				sb.WriteString(text(cell) + ",")
			}
			price := r[len(r)-1]
			switch kind {
			case "hour":
				sb.WriteString(hourly(price))
			case "month":
				sb.WriteString(monthly("1" + price))
			case "hour-discounts":
				sb.WriteString(hourly(price) + "," + hourly("0.001"))
			default:
				sb.WriteString(monthly("9"))
			}
			sb.WriteString(",")
		}
		sb.WriteString(`],` + label(region) + `],`)
	}
	return sb.String()
}

// familyPage is a page of a family: the list of the regions, then the tables.
func familyPage(tables string) string {
	return `<html data-value="Frankfurt (europe-west3)"><script>AF_initDataCallback({data:[` + label("europe-west3") + "," + label("us-central1") + "," + tables + `]});</script></html>`
}

func diskTables(region, balanced, hyper string) string {
	cells := text("Standard provisioned space") + `,"$0.05 / 1 gibibyte month",` + text("Balanced provisioned space") + `,"$` + balanced + ` / 1 gibibyte month",` +
		text("Regional balanced provisioned space") + `,"$0.24 / 1 gibibyte month",` + text("Balanced provisioned IOPS") + `,"$0.2 / 1 month"`
	if hyper != "" {
		cells += "," + text("Hyperdisk Balanced provisioned space") + `,"$` + hyper + ` / 1 gibibyte month"`
	}
	return `[` + cells + `],` + label(region) + `,`
}

// pricing serves the pages of the families and the disks; it counts the requests, and answers the
// default name of Go's HTTP client with nothing, as Google's pages do.
type pricing struct {
	srv      *httptest.Server
	requests atomic.Int32
	status   int
}

// networkPage is the page of the network prices: the row of the addresses in use on standard VMs.
const networkPage = `<html><table><tr><td>Static and ephemeral IP addresses in use on&nbsp; standard VM instances</td><td>$0.005 / 1 hour</td></tr>` +
	`<tr><td>Static and ephemeral IP addresses in use on preemptible and Spot VM instances</td><td>$0.0025 / 1 hour</td></tr></table></html>`

func newPricing(t *testing.T) *pricing {
	t.Helper()
	p := &pricing{status: 200}
	general := familyPage(
		regionTables("europe-west3", [][]string{
			{"e2-standard-4", "4", "16 GiB", "0.134"},
			{"e2-micro", "0.25", "1 GiB", "0.01"}, // a shared core: not a whole number of CPUs
			{"c3-standard-8-lssd", "8", "32 GiB", "375 GiB", "0.9"},
			{"t2a-standard-4", "4", "16 GiB", "0.15"},
			{"c4-standard-4", "4", "15 GiB", "0.2"},
		}) +
			regionTables("us-central1", [][]string{{"e2-standard-4", "4", "16 GiB", "0.1"}}))
	memory := familyPage(regionTables("europe-west3", [][]string{{"m3-ultramem-32", "32", "1,024 GiB", "5.5"}}))
	disks := familyPage(diskTables("europe-west3", "0.12", "0.15") + diskTables("us-central1", "0.1", ""))
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.requests.Add(1)
		if strings.HasPrefix(r.UserAgent(), "Go-http-client") {
			return // nothing
		}
		if p.status != 200 {
			w.WriteHeader(p.status)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/disks-network"):
			_, _ = w.Write([]byte(networkPage))
		case strings.HasPrefix(r.URL.Path, "/families/general-purpose"):
			_, _ = w.Write([]byte(general))
		case strings.HasPrefix(r.URL.Path, "/families/memory-optimized"):
			_, _ = w.Write([]byte(memory))
		case strings.HasPrefix(r.URL.Path, "/disks"):
			_, _ = w.Write([]byte(disks))
		case strings.HasPrefix(r.URL.Path, "/families/compute-optimized"):
			_, _ = w.Write([]byte(familyPage(regionTables("europe-west3", [][]string{{"c2-standard-4", "4", "16 GiB", "0.3"}}))))
		case strings.HasPrefix(r.URL.Path, "/families/changed"):
			_, _ = w.Write([]byte(familyPage("")))
		default: // storage-optimized
			_, _ = w.Write([]byte(familyPage(regionTables("europe-west3", [][]string{{"z3-highmem-8", "8", "64 GiB", "1.0"}}))))
		}
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *pricing) catalog(dir string, now func() time.Time, notice *bytes.Buffer) *gcp.Catalog {
	var w *bytes.Buffer
	if notice != nil {
		w = notice
	}
	if w == nil {
		return gcp.NewWebCatalogWith(nil, nil, p.srv.Client(), p.srv.URL+"/families/", p.srv.URL+"/disks", dir, now, nil)
	}
	return gcp.NewWebCatalogWith(nil, nil, p.srv.Client(), p.srv.URL+"/families/", p.srv.URL+"/disks", dir, now, w)
}

func TestTheMachineTypesAndTheirPricesAreReadFromThePages(t *testing.T) {
	p := newPricing(t)
	offers, err := p.catalog("", time.Now, nil).Offers(context.Background(), "europe-west3-b", 100)
	if err != nil {
		t.Fatal(err)
	}
	byType := map[string]model.Offer{}
	for _, o := range offers {
		byType[o.Type] = o
	}
	names := make([]string, 0, len(byType))
	for n := range byType {
		names = append(names, n)
	}
	sort.Strings(names)
	// the shared core and the type with a local SSD are left out, the other regions are not mixed in
	if got := strings.Join(names, " "); got != "c2-standard-4 c4-standard-4 e2-standard-4 m3-ultramem-32 t2a-standard-4 z3-highmem-8" {
		t.Fatalf("%s", got)
	}
	e2 := byType["e2-standard-4"]
	// 0.134 per hour and 100 GB of balanced disk at 0.12 a month
	if e2.Provider != "gcp" || e2.Location != "europe-west3-b" || e2.VCPU != 4 || e2.RAMGB != 16 || e2.Arch != "x86" || e2.DiskGB != 0 || e2.SysDiskGB != 100 ||
		e2.Currency != "USD" || e2.VATPercent != 0 || abs(e2.HourlyPrice-(0.134+0.005+100*0.12/730)) > 1e-9 || abs(e2.MonthlyPrice-((0.134+0.005)*730+12)) > 1e-9 || !e2.IPv4 {
		t.Errorf("%+v", e2)
	}
	if byType["t2a-standard-4"].Arch != "arm" || byType["m3-ultramem-32"].RAMGB != 1024 {
		t.Errorf("%+v %+v", byType["t2a-standard-4"], byType["m3-ultramem-32"])
	}
	// a type of the newer series takes the Hyperdisk
	if c4 := byType["c4-standard-4"]; abs(c4.HourlyPrice-(0.2+0.005+100*0.15/730)) > 1e-9 {
		t.Errorf("%+v", c4)
	}
	// another region has its own prices, and no Hyperdisk price: those types are left out
	us, err := p.catalog("", time.Now, nil).Offers(context.Background(), "us-central1-a", 0)
	if err != nil || len(us) != 1 || abs(us[0].HourlyPrice-(0.1+0.005+50*0.1/730)) > 1e-9 || us[0].SysDiskGB != 50 {
		t.Errorf("%+v %v", us, err)
	}
	if _, err := p.catalog("", time.Now, nil).Offers(context.Background(), "mars-north1-a", 0); err == nil || !strings.Contains(err.Error(), "no prices for region mars-north1") {
		t.Errorf("%v", err)
	}
}

func TestThePagesAreReadOnceADay(t *testing.T) {
	p := newPricing(t)
	dir := t.TempDir()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	var notice bytes.Buffer
	c := p.catalog(dir, clock, &notice)
	ask := func(c *gcp.Catalog) error { _, err := c.Offers(context.Background(), "europe-west3-b", 0); return err }
	if err := ask(c); err != nil {
		t.Fatal(err)
	}
	first := p.requests.Load()
	now = now.Add(23 * time.Hour)
	if err := ask(p.catalog(dir, clock, nil)); err != nil || p.requests.Load() != first {
		t.Errorf("within a day: %v %d requests", err, p.requests.Load()-first)
	}
	if strings.Count(notice.String(), "reading the public pricing pages") != 1 {
		t.Errorf("%q", notice.String())
	}
	now = now.Add(2 * time.Hour)
	if err := ask(c); err != nil || p.requests.Load() == first {
		t.Errorf("after a day: %v %d requests", err, p.requests.Load()-first)
	}
	// the pages cannot be read any more: the old digest is better than none
	p.status = 503
	now = now.Add(48 * time.Hour)
	if err := ask(c); err != nil {
		t.Errorf("old digest: %v", err)
	}
}

func TestPagesWithoutPricesAreAnError(t *testing.T) {
	p := newPricing(t)
	p.status = 503
	if _, err := p.catalog("", time.Now, nil).Offers(context.Background(), "europe-west3-b", 0); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Errorf("%v", err)
	}
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>changed</html>")) }))
	defer empty.Close()
	c := gcp.NewWebCatalogWith(nil, nil, empty.Client(), empty.URL+"/f/", empty.URL+"/d", "", time.Now, nil)
	if _, err := c.Offers(context.Background(), "europe-west3-b", 0); err == nil || !strings.Contains(err.Error(), "did it change") {
		t.Errorf("%v", err)
	}
}

func TestTheProjectIsAskedFirstAndThePagesAreTheFallback(t *testing.T) {
	p := newPricing(t)
	var notice bytes.Buffer
	types := func(context.Context, string) ([]*compute.MachineType, error) {
		return []*compute.MachineType{{Name: "e2-standard-4", GuestCpus: 4, MemoryMb: 16384}}, nil
	}
	skus := func(context.Context, string) ([]*cloudbilling.Sku, error) {
		return nil, fmt.Errorf("billing api disabled")
	}
	c := gcp.NewWebCatalogWith(types, skus, p.srv.Client(), p.srv.URL+"/families/", p.srv.URL+"/disks", "", time.Now, &notice)
	offers, err := c.Offers(context.Background(), "europe-west3-b", 0)
	if err != nil || len(offers) != 6 || !strings.Contains(notice.String(), "using the public pricing pages") {
		t.Errorf("%d offers, %v, %q", len(offers), err, notice.String())
	}
}

func TestWithoutPagesTheProjectErrorIsTheAnswer(t *testing.T) {
	skus := func(context.Context, string) ([]*cloudbilling.Sku, error) {
		return nil, fmt.Errorf("billing api disabled")
	}
	types := func(context.Context, string) ([]*compute.MachineType, error) { return nil, nil }
	if _, err := gcp.NewCatalogWith(types, skus).Offers(context.Background(), "europe-west3-b", 0); err == nil || err.Error() != "billing api disabled" {
		t.Errorf("%v", err)
	}
}

func TestTheOnDemandPriceIsTheHighestOfTheRow(t *testing.T) {
	// a table with discounts that comes first and has the on-demand price in the last column
	page := familyPage(`[[` + text("e2-standard-4") + `,` + text("4") + `,` + text("16 GiB") + `,` + hourly("0.05") + `,` + hourly("0.134") + `,` + hourly("0.08") + `,` +
		text("e2-standard-8") + `,` + text("8") + `,` + text("32 GiB") + `,` + hourly("0.1") + `,` + hourly("0.268") + `],` + label("europe-west3") + `],`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/disks-network"):
			_, _ = w.Write([]byte(networkPage))
		case strings.HasPrefix(r.URL.Path, "/disks"):
			_, _ = w.Write([]byte(familyPage(diskTables("europe-west3", "0.12", "0.15"))))
		default:
			_, _ = w.Write([]byte(page))
		}
	}))
	defer srv.Close()
	c := gcp.NewWebCatalogWith(nil, nil, srv.Client(), srv.URL+"/f/", srv.URL+"/disks", "", time.Now, nil)
	offers, err := gcp.WithPages(c, []string{"general-purpose"}).Offers(context.Background(), "europe-west3-b", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range offers {
		want := map[string]float64{"e2-standard-4": 0.134, "e2-standard-8": 0.268}[o.Type]
		if abs(o.HourlyPrice-(want+0.005+100*0.12/730)) > 1e-9 {
			t.Errorf("%s: %v, want on-demand %v", o.Type, o.HourlyPrice, want)
		}
	}
}

func TestAChangedPageIsNotKept(t *testing.T) {
	p := newPricing(t)
	dir := t.TempDir()
	// one of the family pages gives nothing: no digest, and so none in the cache
	c := gcp.NewWebCatalogWith(nil, nil, p.srv.Client(), p.srv.URL+"/families/", p.srv.URL+"/disks", dir, time.Now, nil)
	c = gcp.WithPages(c, []string{"general-purpose", "changed"})
	if _, err := c.Offers(context.Background(), "europe-west3-b", 0); err == nil || !strings.Contains(err.Error(), "pricing page changed") {
		t.Errorf("%v", err)
	}
	// the price of the address may be kept, the digest of the pages not
	if files, _ := filepath.Glob(filepath.Join(dir, "gcp-web.json")); len(files) != 0 {
		t.Errorf("cached: %v", files)
	}
}
