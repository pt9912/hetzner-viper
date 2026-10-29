package azure_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/azure"
)

func disk(tier string, price float64) map[string]any {
	return map[string]any{"currencyCode": "EUR", "retailPrice": price, "unitOfMeasure": "1/Month", "productName": "Standard SSD Managed Disks", "skuName": tier + " LRS", "meterName": tier + " LRS Disk", "type": "Consumption"}
}

func item(sku, product, skuName string, price float64) map[string]any {
	return map[string]any{"currencyCode": "EUR", "retailPrice": price, "unitOfMeasure": "1 Hour", "armSkuName": sku, "productName": product, "skuName": skuName, "type": "Consumption"}
}

// retail is the public price list: it counts the requests and keeps the first filter and currency.
type retail struct {
	srv              *httptest.Server
	requests         atomic.Int32
	filter, currency string
	status           int
}

func newRetail(t *testing.T) *retail {
	t.Helper()
	r := &retail{status: 200}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.requests.Add(1)
		q := req.URL.Query()
		if r.status != 200 {
			w.WriteHeader(r.status)
			return
		}
		switch {
		case strings.Contains(q.Get("$filter"), "'Storage'"):
			_ = json.NewEncoder(w).Encode(map[string]any{"Items": []any{disk("E10", 9.0), disk("E6", 5.0), disk("E4", 2.5), disk("E15", 17.0)}})
		case q.Get("page") == "2":
			_ = json.NewEncoder(w).Encode(map[string]any{"Items": []any{item("Standard_D4ps_v5", "Virtual Machines Dpsv5 Series", "D4ps v5", 0.15)}})
		default:
			r.filter, r.currency = q.Get("$filter"), q.Get("currencyCode")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Items": []any{
					item("Standard_D4as_v5", "Virtual Machines Dasv5 Series", "D4as v5", 0.20),
					item("Standard_D4as_v5", "Virtual Machines Dasv5 Series Windows", "D4as v5", 0.40),
					item("Standard_D4as_v5", "Virtual Machines Dasv5 Series", "D4as v5 Spot", 0.02),
					item("Standard_D4as_v5", "Virtual Machines Dasv5 Series", "D4as v5 Low Priority", 0.03),
					item("Standard_NoSize", "Virtual Machines", "x", 1),
				},
				"NextPageLink": r.srv.URL + "/?page=2",
			})
		}
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func specs(m map[string]azure.Spec) azure.Specs {
	return func(context.Context, string) (map[string]azure.Spec, error) { return m, nil }
}

// sizes are the sizes of three VM sizes, one of which has no price.
func sizes() map[string]azure.Spec {
	return map[string]azure.Spec{
		azure.Key("Standard_D4as_v5"):  {VCPUs: 4, RAMGB: 16},
		azure.Key("Standard_D4ps_v5"):  {VCPUs: 4, RAMGB: 16},
		azure.Key("Standard_Unpriced"): {VCPUs: 2, RAMGB: 4},
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestKeyMakesTheSpellingsAlike(t *testing.T) {
	if azure.Key("Standard_D4as_v5") != "d4asv5" || azure.Key("d4asv5") != "d4asv5" || azure.Key("Standard_NC24ads_A100_v4") != "nc24adsa100v4" {
		t.Errorf("%q %q", azure.Key("Standard_D4as_v5"), azure.Key("Standard_NC24ads_A100_v4"))
	}
}

func TestCatalogJoinsTheSizesWithTheLinuxPrices(t *testing.T) {
	r := newRetail(t)
	c := azure.NewCatalogWith(nil, specs(sizes()), r.srv.Client(), r.srv.URL, "", time.Now, nil)
	offers, err := c.Offers(context.Background(), "germanywestcentral", 80)
	if err != nil || len(offers) != 2 {
		t.Fatalf("%+v %v", offers, err)
	}
	sort.Slice(offers, func(i, j int) bool { return offers[i].Type < offers[j].Type })
	a, p := offers[0], offers[1]
	// the disk of 80 GB costs what the tier of 128 GB costs
	if a.Type != "Standard_D4as_v5" || a.Provider != "azure" || a.Arch != "x86" || a.VCPU != 4 || a.RAMGB != 16 || a.DiskGB != 0 || a.SysDiskGB != 80 ||
		abs(a.HourlyPrice-(0.20+9.0/730)) > 1e-9 || abs(a.MonthlyPrice-(0.20*730+9.0)) > 1e-9 || a.Currency != "EUR" || a.VATPercent != 0 {
		t.Errorf("%+v", a)
	}
	if p.Arch != "arm" || abs(p.HourlyPrice-(0.15+9.0/730)) > 1e-9 {
		t.Errorf("%+v", p)
	}
	if r.currency != "EUR" || !strings.Contains(r.filter, "armRegionName eq 'germanywestcentral'") {
		t.Errorf("%q %q", r.currency, r.filter)
	}
	// 64 GB without a given size: the tier of 64 GB
	offers, err = c.Offers(context.Background(), "germanywestcentral", 0)
	if err != nil || offers[0].SysDiskGB != 64 {
		t.Errorf("%+v %v", offers, err)
	}
}

func TestTheSubscriptionIsAskedFirstAndTheWebsiteIsTheFallback(t *testing.T) {
	r := newRetail(t)
	var notice bytes.Buffer
	failing := func(context.Context, string) (map[string]azure.Spec, error) { return nil, errors.New("no login") }
	// the subscription knows only one of the sizes; the website both
	one := specs(map[string]azure.Spec{azure.Key("Standard_D4as_v5"): {VCPUs: 4, RAMGB: 16}})
	c := azure.NewCatalogWith(one, specs(sizes()), r.srv.Client(), r.srv.URL, "", time.Now, &notice)
	if offers, err := c.Offers(context.Background(), "x", 0); err != nil || len(offers) != 1 || notice.Len() != 0 {
		t.Errorf("subscription: %d %v %q", len(offers), err, notice.String())
	}
	c = azure.NewCatalogWith(failing, specs(sizes()), r.srv.Client(), r.srv.URL, "", time.Now, &notice)
	if offers, err := c.Offers(context.Background(), "x", 0); err != nil || len(offers) != 2 || !strings.Contains(notice.String(), "using the data of the Azure website") {
		t.Errorf("fallback: %d %v %q", len(offers), err, notice.String())
	}
}

func TestCatalogReadsThePriceListsOnceADay(t *testing.T) {
	r := newRetail(t)
	dir := t.TempDir()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	c := azure.NewCatalogWith(nil, specs(sizes()), r.srv.Client(), r.srv.URL, dir, clock, nil)
	ask := func(c *azure.Catalog) error {
		_, err := c.Offers(context.Background(), "germanywestcentral", 0)
		return err
	}
	if err := ask(c); err != nil {
		t.Fatal(err)
	}
	first := r.requests.Load()
	now = now.Add(23 * time.Hour)
	if err := ask(c); err != nil || r.requests.Load() != first {
		t.Errorf("within a day: %v %d requests", err, r.requests.Load()-first)
	}
	// another run uses the file
	if err := ask(azure.NewCatalogWith(nil, specs(sizes()), r.srv.Client(), r.srv.URL, dir, clock, nil)); err != nil || r.requests.Load() != first {
		t.Errorf("new catalog: %v %d requests", err, r.requests.Load()-first)
	}
	now = now.Add(2 * time.Hour)
	if err := ask(c); err != nil || r.requests.Load() == first {
		t.Errorf("after a day: %v %d requests", err, r.requests.Load()-first)
	}
	// the list cannot be read any more: the old digest is better than none
	r.status = 503
	now = now.Add(48 * time.Hour)
	if offers, err := c.Offers(context.Background(), "germanywestcentral", 0); err != nil || len(offers) != 2 {
		t.Errorf("old digest: %d %v", len(offers), err)
	}
}

func TestCatalogReportsAFailedPriceList(t *testing.T) {
	r := newRetail(t)
	r.status = 503
	c := azure.NewCatalogWith(nil, specs(sizes()), r.srv.Client(), r.srv.URL, "", time.Now, nil)
	if _, err := c.Offers(context.Background(), "x", 0); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Errorf("%v", err)
	}
	if _, err := c.Offers(context.Background(), "", 0); err == nil {
		t.Error("no location: want an error")
	}
}

func TestTheSizesAreReadFromTheCalculatorOnceADay(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"offers":{
			"linux-d4asv5-standard":{"cores":4,"ram":16.0},
			"windows-d4asv5-standard":{"cores":4,"ram":16.0,"diskSize":1},
			"linux-d4asv5-lowpriority":{"cores":4,"ram":16.0},
			"linux-a0-basic":{"cores":1,"ram":0.75},
			"linux-b1ls-standard":{"cores":0.5,"ram":0.5},
			"linux-d4psv5-standard":{"cores":4,"ram":16.0}},"regions":[]}`))
	}))
	defer srv.Close()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	var notice bytes.Buffer
	c := azure.NewCatalogWith(nil, nil, srv.Client(), "", t.TempDir(), func() time.Time { return now }, &notice)
	read := azure.CalculatorSpecs(c, srv.URL)
	got, err := read(context.Background(), "x")
	if err != nil || len(got) != 2 || got["d4asv5"] != (azure.Spec{VCPUs: 4, RAMGB: 16}) || got["d4psv5"].VCPUs != 4 {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := read(context.Background(), "y"); err != nil || requests.Load() != 1 {
		t.Errorf("the second look is served from the cache: %v %d requests", err, requests.Load())
	}
	if strings.Count(notice.String(), "reading the VM sizes") != 1 {
		t.Errorf("%q", notice.String())
	}
	now = now.Add(25 * time.Hour)
	if _, err := read(context.Background(), "y"); err != nil || requests.Load() != 2 {
		t.Errorf("after a day: %v %d requests", err, requests.Load())
	}
}

func TestAnEmptyAnswerOfTheSubscriptionIsTheFallbackToo(t *testing.T) {
	r := newRetail(t)
	var notice bytes.Buffer
	empty := specs(map[string]azure.Spec{})
	c := azure.NewCatalogWith(empty, specs(sizes()), r.srv.Client(), r.srv.URL, "", time.Now, &notice)
	if offers, err := c.Offers(context.Background(), "x", 0); err != nil || len(offers) != 2 || !strings.Contains(notice.String(), "using the data of the Azure website") {
		t.Errorf("%d %v %q", len(offers), err, notice.String())
	}
}

func TestTheLocationIsAFileNameOfTheCache(t *testing.T) {
	r := newRetail(t)
	c := azure.NewCatalogWith(nil, specs(sizes()), r.srv.Client(), r.srv.URL, t.TempDir(), time.Now, nil)
	for _, loc := range []string{"", "../x", "a/b", "Germany West", "."} {
		if _, err := c.Offers(context.Background(), loc, 0); err == nil || r.requests.Load() != 0 {
			t.Errorf("%q: %v, %d requests", loc, err, r.requests.Load())
		}
	}
}
