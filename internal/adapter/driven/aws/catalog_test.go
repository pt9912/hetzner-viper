package aws_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/aws"
	"github.com/pt9912/hetzner-viper/internal/model"
)

// product and term are the entries of a price list.
func product(id, family, typ, os, vcpu, mem, cpu, capacity string) string {
	return fmt.Sprintf(`%q:{"sku":%q,"productFamily":%q,"attributes":{"instanceType":%q,"vcpu":%q,"memory":%q,"physicalProcessor":%q,"tenancy":"Shared","operatingSystem":%q,"licenseModel":"No License required","operation":"RunInstances","preInstalledSw":"NA","capacitystatus":%q,"locationType":"AWS Region"}}`,
		id, id, family, typ, vcpu, mem, cpu, os, capacity)
}

func volume(id, api string) string {
	return fmt.Sprintf(`%q:{"sku":%q,"productFamily":"Storage","attributes":{"volumeApiName":%q}}`, id, id, api)
}

func term(id, unit, usd string) string {
	return fmt.Sprintf(`%q:{"JRTCKXETXF":{"priceDimensions":{"JRTCKXETXF.6YS6EN2CT7":{"unit":%q,"pricePerUnit":{"USD":%q}}}}}`, id, unit, usd)
}

// priceList is a price list as AWS publishes it: the products first, then the terms.
func priceList() string {
	products := []string{
		product("A1", "Compute Instance", "m7i.large", "Linux", "2", "8 GiB", "Intel Xeon", "Used"),
		product("A2", "Compute Instance", "m7g.xlarge", "Linux", "4", "16 GiB", "AWS Graviton3 Processor", "Used"),
		product("A3", "Compute Instance", "x2.big", "Linux", "64", "1,024 GiB", "Intel Xeon", "Used"),
		product("W1", "Compute Instance", "m7i.large", "Windows", "2", "8 GiB", "Intel Xeon", "Used"),
		product("R1", "Compute Instance", "m7i.large", "Linux", "2", "8 GiB", "Intel Xeon", "UnusedCapacityReservation"),
		product("M1", "Compute Instance (bare metal)", "m7i.metal", "Linux", "96", "384 GiB", "Intel Xeon", "Used"),
		product("U1", "Compute Instance", "unpriced.large", "Linux", "2", "4 GiB", "Intel Xeon", "Used"),
		volume("V1", "gp3"), volume("V2", "gp2"),
	}
	terms := []string{
		term("A1", "Hrs", "0.1134"), term("A2", "Hrs", "0.1632"), term("A3", "Hrs", "9.5"),
		term("W1", "Hrs", "0.2"), term("R1", "Hrs", "0.0001"), term("M1", "Hrs", "5"),
		term("V1", "GB-Mo", "0.0952"), term("V2", "GB-Mo", "0.119"),
	}
	return `{"formatVersion":"v1.0","disclaimer":"x","offerCode":"AmazonEC2","products":{` + strings.Join(products, ",") +
		`},"terms":{"Reserved":{"A1":{"huge":{"priceDimensions":{}}}},"OnDemand":{` + strings.Join(terms, ",") + `}},"attributesList":{"x":[1,2,{"y":null}]}}`
}

// server serves the price list and counts the requests.
func server(t *testing.T, body string, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if r.URL.Path != "/eu-central-1/index.json" {
			w.WriteHeader(403)
			return
		}
		if status != 200 {
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestCatalogReadsTheSizesAndPricesOfAPriceList(t *testing.T) {
	srv, _ := server(t, priceList(), 200)
	c := aws.NewCatalogWith(t.TempDir(), srv.Client(), srv.URL+"/", time.Now, nil)
	offers, err := c.Offers(context.Background(), "eu-central-1a", 80)
	if err != nil || len(offers) != 3 {
		t.Fatalf("%+v %v", offers, err)
	}
	sort.Slice(offers, func(i, j int) bool { return offers[i].Type < offers[j].Type })
	var g model.Offer
	for _, o := range offers {
		if o.Type == "m7g.xlarge" {
			g = o
		}
	}
	if g.Provider != "aws" || g.Location != "eu-central-1a" || g.VCPU != 4 || g.RAMGB != 16 || g.Arch != "arm" || g.DiskGB != 0 || g.SysDiskGB != 80 || g.Currency != "USD" || g.VATPercent != 0 {
		t.Errorf("%+v", g)
	}
	if want := 0.1632 + 80*0.0952/730; abs(g.HourlyPrice-want) > 1e-9 {
		t.Errorf("hourly %v, want %v", g.HourlyPrice, want)
	}
	for _, o := range offers {
		switch o.Type {
		case "m7i.large": // not the Windows price, not the unused reservation
			if o.Arch != "x86" || abs(o.MonthlyPrice-(0.1134*730+80*0.0952)) > 1e-9 {
				t.Errorf("%+v", o)
			}
		case "x2.big":
			if o.RAMGB != 1024 {
				t.Errorf("1,024 GiB: %+v", o)
			}
		case "m7g.xlarge":
		default:
			t.Errorf("unexpected %s", o.Type)
		}
	}
	// 50 GB without a given size
	offers, err = c.Offers(context.Background(), "eu-central-1", 0)
	if err != nil || offers[0].SysDiskGB != 50 {
		t.Errorf("%+v %v", offers, err)
	}
}

func TestCatalogReadsThePriceListOnceADay(t *testing.T) {
	srv, requests := server(t, priceList(), 200)
	dir := t.TempDir()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	c := aws.NewCatalogWith(dir, srv.Client(), srv.URL+"/", clock, nil)
	ask := func() error { _, err := c.Offers(context.Background(), "eu-central-1", 0); return err }
	if err := ask(); err != nil || requests.Load() != 1 {
		t.Fatalf("first: %v %d", err, requests.Load())
	}
	now = now.Add(23 * time.Hour)
	if err := ask(); err != nil || requests.Load() != 1 {
		t.Errorf("within a day: %v %d requests", err, requests.Load())
	}
	// another run (a new catalog) uses the file
	if _, err := aws.NewCatalogWith(dir, srv.Client(), srv.URL+"/", clock, nil).Offers(context.Background(), "eu-central-1", 0); err != nil || requests.Load() != 1 {
		t.Errorf("new catalog: %v %d requests", err, requests.Load())
	}
	now = now.Add(2 * time.Hour)
	if err := ask(); err != nil || requests.Load() != 2 {
		t.Errorf("after a day: %v %d requests", err, requests.Load())
	}
}

func TestAnOldDigestIsBetterThanNone(t *testing.T) {
	srv, _ := server(t, priceList(), 200)
	dir := t.TempDir()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	c := aws.NewCatalogWith(dir, srv.Client(), srv.URL+"/", func() time.Time { return now }, nil)
	if _, err := c.Offers(context.Background(), "eu-central-1", 0); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	now = now.Add(72 * time.Hour)
	if offers, err := c.Offers(context.Background(), "eu-central-1", 0); err != nil || len(offers) != 3 {
		t.Errorf("the list cannot be read: %d %v", len(offers), err)
	}
	// without a digest the failure is the answer
	other := aws.NewCatalogWith(t.TempDir(), srv.Client(), srv.URL+"/", time.Now, nil)
	if _, err := other.Offers(context.Background(), "eu-central-1", 0); err == nil {
		t.Error("no digest, no list: want an error")
	}
}

func TestCatalogReportsWhatIsWrongWithThePriceList(t *testing.T) {
	for name, c := range map[string]struct {
		body   string
		status int
		want   string
	}{
		"unknown region": {"", 403, "no price list for region eu-central-1"},
		"server error":   {"", 503, "HTTP 503"},
		"not json":       {"<html>", 200, "price list"},
		"no types":       {`{"products":{},"terms":{"OnDemand":{}}}`, 200, "no instance types"},
		"no volume price": {`{"products":{` + product("A1", "Compute Instance", "m7i.large", "Linux", "2", "8 GiB", "Intel Xeon", "Used") +
			`},"terms":{"OnDemand":{` + term("A1", "Hrs", "0.1") + `}}}`, 200, "gp3"},
	} {
		srv, _ := server(t, c.body, c.status)
		_, err := aws.NewCatalogWith("", srv.Client(), srv.URL+"/", time.Now, nil).Offers(context.Background(), "eu-central-1", 0)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	c := aws.NewCatalogWith("", nil, "", time.Now, nil)
	for _, loc := range []string{"", "../x", "a/b", "eu central"} {
		if _, err := c.Offers(context.Background(), loc, 0); err == nil {
			t.Errorf("%q: want an error", loc)
		}
	}
}

func TestCatalogSaysThatItReadsALargeFile(t *testing.T) {
	srv, _ := server(t, priceList(), 200)
	var notice bytes.Buffer
	c := aws.NewCatalogWith(t.TempDir(), srv.Client(), srv.URL+"/", time.Now, &notice)
	for range 2 {
		if _, err := c.Offers(context.Background(), "eu-central-1", 0); err != nil {
			t.Fatal(err)
		}
	}
	// once: the second look is served from the cache
	if got := notice.String(); strings.Count(got, "reading the price list of eu-central-1") != 1 {
		t.Errorf("%q", got)
	}
}

// flaky serves the price list but breaks off the first n answers halfway, as a connection can.
func flaky(t *testing.T, breaks int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	body := priceList()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if int(n.Add(1)) <= breaks {
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			_, _ = w.Write([]byte(body[:len(body)/2])) // less than it promised: the client sees the body end early
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestADownloadThatBreaksOffIsTriedAgain(t *testing.T) {
	srv, requests := flaky(t, 2)
	var notice bytes.Buffer
	c := aws.NewCatalogWith("", srv.Client(), srv.URL+"/", time.Now, &notice)
	offers, err := c.Offers(context.Background(), "eu-central-1", 0)
	if err != nil || len(offers) != 3 || requests.Load() != 3 {
		t.Fatalf("%d offers, %v, %d requests", len(offers), err, requests.Load())
	}
	if !strings.Contains(notice.String(), "trying again (3/3)") {
		t.Errorf("%q", notice.String())
	}
}

func TestADownloadThatKeepsBreakingOffGivesAClearError(t *testing.T) {
	srv, requests := flaky(t, 99)
	c := aws.NewCatalogWith("", srv.Client(), srv.URL+"/", time.Now, nil)
	_, err := c.Offers(context.Background(), "eu-central-1", 0)
	if err == nil || !strings.Contains(err.Error(), "broke off 3 times") || requests.Load() != 3 {
		t.Errorf("%v, %d requests", err, requests.Load())
	}
}

func TestAWrongAnswerIsNotTriedAgain(t *testing.T) {
	srv, requests := server(t, "", 503)
	c := aws.NewCatalogWith("", srv.Client(), srv.URL+"/", time.Now, nil)
	if _, err := c.Offers(context.Background(), "eu-central-1", 0); err == nil || requests.Load() != 1 {
		t.Errorf("%v, %d requests", err, requests.Load())
	}
}
