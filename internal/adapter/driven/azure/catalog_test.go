package azure_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/azure"
)

func disk(tier string, price float64) map[string]any {
	return map[string]any{"currencyCode": "EUR", "retailPrice": price, "unitOfMeasure": "1/Month", "productName": "Standard SSD Managed Disks", "skuName": tier + " LRS", "meterName": tier + " LRS Disk", "type": "Consumption"}
}

func item(sku, product, skuName string, price float64) map[string]any {
	return map[string]any{"currencyCode": "EUR", "retailPrice": price, "unitOfMeasure": "1 Hour", "armSkuName": sku, "productName": product, "skuName": skuName, "type": "Consumption"}
}

func TestCatalogJoinsTheSizesWithTheLinuxPrices(t *testing.T) {
	var filter, currency string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("$filter"), "'Storage'") {
			_ = json.NewEncoder(w).Encode(map[string]any{"Items": []any{disk("E10", 9.0), disk("E6", 5.0), disk("E4", 2.5), disk("E15", 17.0)}})
			return
		}
		if r.URL.Query().Get("page") != "2" {
			filter, currency = r.URL.Query().Get("$filter"), r.URL.Query().Get("currencyCode")
		}
		if r.URL.Query().Get("page") == "2" {
			_ = json.NewEncoder(w).Encode(map[string]any{"Items": []any{item("Standard_D4ps_v5", "Virtual Machines Dpsv5 Series", "D4ps v5", 0.15)}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Items": []any{
				item("Standard_D4as_v5", "Virtual Machines Dasv5 Series", "D4as v5", 0.20),
				item("Standard_D4as_v5", "Virtual Machines Dasv5 Series Windows", "D4as v5", 0.40),
				item("Standard_D4as_v5", "Virtual Machines Dasv5 Series", "D4as v5 Spot", 0.02),
				item("Standard_D4as_v5", "Virtual Machines Dasv5 Series", "D4as v5 Low Priority", 0.03),
				item("Standard_NoSize", "Virtual Machines", "x", 1),
			},
			"NextPageLink": srv.URL + "/?page=2",
		})
	}))
	defer srv.Close()
	sizes := func(_ context.Context, loc string) ([]azure.SizeInfo, error) {
		if loc != "germanywestcentral" {
			t.Errorf("location %q", loc)
		}
		return []azure.SizeInfo{{Name: "Standard_D4as_v5", VCPUs: 4, MemoryMB: 16384}, {Name: "Standard_D4ps_v5", VCPUs: 4, MemoryMB: 16384}, {Name: "Standard_Unpriced", VCPUs: 2, MemoryMB: 4096}}, nil
	}
	offers, err := azure.NewCatalogWith(sizes, srv.Client(), srv.URL).Offers(context.Background(), "germanywestcentral", 80)
	if err != nil || len(offers) != 2 {
		t.Fatalf("%+v %v", offers, err)
	}
	sort.Slice(offers, func(i, j int) bool { return offers[i].Type < offers[j].Type })
	a, p := offers[0], offers[1]
	if a.Provider != "azure" || a.Arch != "x86" || a.VCPU != 4 || a.RAMGB != 16 || a.DiskGB != 0 || a.SysDiskGB != 80 || abs(a.HourlyPrice-(0.20+9.0/730)) > 1e-9 || a.Currency != "EUR" || a.VATPercent != 0 {
		t.Errorf("%+v", a)
	}
	if p.Arch != "arm" || abs(p.HourlyPrice-(0.15+9.0/730)) > 1e-9 {
		t.Errorf("%+v", p)
	}
	if currency != "EUR" || !strings.Contains(filter, "armRegionName eq 'germanywestcentral'") {
		t.Errorf("%q %q", currency, filter)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestCatalogReportsAFailedPriceList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	sizes := func(context.Context, string) ([]azure.SizeInfo, error) { return nil, nil }
	if _, err := azure.NewCatalogWith(sizes, srv.Client(), srv.URL).Offers(context.Background(), "x", 0); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Errorf("%v", err)
	}
	if _, err := azure.NewCatalogWith(sizes, srv.Client(), srv.URL).Offers(context.Background(), "", 0); err == nil {
		t.Error("no location: want an error")
	}
}
