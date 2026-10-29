package find_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/fx"
	"github.com/pt9912/hetzner-viper/internal/usecase/find"
)

type fakeCatalog struct {
	offers []model.Offer
	err    error
}

func (f fakeCatalog) Offers(context.Context, string, int) ([]model.Offer, error) {
	return f.offers, f.err
}

func offer(typ, loc string, cpu int, ram float64, disk int, monthly float64) model.Offer {
	return model.Offer{Provider: "hetzner", Type: typ, Location: loc, VCPU: cpu, RAMGB: ram, DiskGB: disk, Arch: "x86", HourlyPrice: monthly / 700, MonthlyPrice: monthly, Currency: "EUR"}
}

func run(s find.Service, need model.Need) (int, string, string) {
	var out, errOut bytes.Buffer
	n := s.Find(context.Background(), need, &out, &errOut)
	return n, out.String(), errOut.String()
}

func TestShowsTheCheapestThatFit(t *testing.T) {
	cat := fakeCatalog{offers: []model.Offer{
		offer("big", "fsn1", 8, 16, 240, 30),
		offer("small", "fsn1", 2, 4, 40, 5),
		offer("mid", "fsn1", 4, 8, 80, 12),
		offer("mid", "hel1", 4, 8, 80, 11), // cheaper elsewhere: this location is shown
		offer("mid2", "fsn1", 4, 16, 160, 20),
	}}
	n, out, errOut := run(find.Service{Deps: find.Deps{Sources: []find.Source{{Name: "hetzner", Catalog: cat}}, Top: 2}}, model.Need{VCPU: 4, RAMGB: 8, DiskGB: 80})
	if n != 2 || errOut != "" {
		t.Fatalf("%d %q", n, errOut)
	}
	if strings.Contains(out, "small") || strings.Contains(out, "big") {
		t.Errorf("types that do not fit, or beyond the top 2:\n%s", out)
	}
	if !strings.Contains(out, "mid2") || !strings.Contains(out, "hel1") || strings.Contains(out, "fsn1       4") {
		t.Errorf("mid should be shown in hel1 only:\n%s", out)
	}
	if !strings.Contains(out, "\n  PROVIDER=hetzner SERVER_TYPE=mid SERVER_LOCATION=hel1\n") {
		t.Errorf("no line to copy:\n%s", out)
	}
}

func TestDiskChosenApartIsSetAsASetting(t *testing.T) {
	o := offer("m7i.xlarge", "eu-central-1", 4, 16, 0, 100)
	o.Provider, o.SysDiskGB, o.Currency = "aws", 80, "USD"
	_, out, _ := run(find.Service{Deps: find.Deps{Sources: []find.Source{{Name: "aws", Catalog: fakeCatalog{offers: []model.Offer{o}}}}}}, model.Need{VCPU: 4, DiskGB: 80})
	if !strings.Contains(out, "SERVER_LOCATION=eu-central-1 AWS_ROOT_GB=80\n") || !strings.Contains(out, "80*") || !strings.Contains(out, "$") || !strings.Contains(out, "* system disk") {
		t.Errorf("%s", out)
	}
}

func TestLeavesOutWhatCannotBeRead(t *testing.T) {
	good := fakeCatalog{offers: []model.Offer{offer("a", "fsn1", 2, 4, 40, 5)}}
	s := find.Service{Deps: find.Deps{Sources: []find.Source{
		{Name: "aws", Skip: "no price list yet"},
		{Name: "azure", Catalog: fakeCatalog{err: errors.New("boom")}, Describe: func(error) string { return "described" }},
		{Name: "gcp", Catalog: fakeCatalog{}},
		{Name: "hetzner", Catalog: good},
	}}}
	n, _, errOut := run(s, model.Need{VCPU: 1})
	if n != 1 {
		t.Errorf("shown %d", n)
	}
	for _, want := range []string{"skipped aws: no price list yet", "skipped azure: described", "gcp: no server type fits"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("missing %q in %q", want, errOut)
		}
	}
}

func TestNothingFits(t *testing.T) {
	cat := fakeCatalog{offers: []model.Offer{offer("a", "fsn1", 2, 4, 40, 5)}}
	if n, out, _ := run(find.Service{Deps: find.Deps{Sources: []find.Source{{Name: "hetzner", Catalog: cat}}}}, model.Need{VCPU: 64}); n != 0 || out != "" {
		t.Errorf("%d %q", n, out)
	}
}

// slowCatalog answers only when all the others are asking too: one at a time it would never.
type slowCatalog struct {
	asking *sync.WaitGroup
	offers []model.Offer
}

func (s slowCatalog) Offers(context.Context, string, int) ([]model.Offer, error) {
	s.asking.Done()
	s.asking.Wait()
	return s.offers, nil
}

func TestTheProvidersAreAskedAtTheSameTimeAndShownInOrder(t *testing.T) {
	var asking sync.WaitGroup
	asking.Add(3)
	mk := func(name, typ string) find.Source {
		o := offer(typ, "x", 2, 4, 40, 5)
		o.Provider = name
		return find.Source{Name: name, Catalog: slowCatalog{&asking, []model.Offer{o}}}
	}
	done := make(chan string)
	go func() {
		_, out, _ := run(find.Service{Deps: find.Deps{Sources: []find.Source{mk("aws", "a1"), mk("azure", "b1"), mk("hetzner", "c1")}}}, model.Need{VCPU: 1})
		done <- out
	}()
	select {
	case out := <-done:
		a, b, c := strings.Index(out, "aws "), strings.Index(out, "azure "), strings.Index(out, "hetzner ")
		if a < 0 || b < a || c < b {
			t.Errorf("order:\n%s", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the providers are asked one after the other")
	}
}

func TestOnlyTheNamedProviderIsAsked(t *testing.T) {
	s := find.Service{Deps: find.Deps{Sources: []find.Source{
		{Name: "aws", Skip: "no credentials"},
		{Name: "hetzner", Catalog: fakeCatalog{offers: []model.Offer{offer("a", "fsn1", 2, 4, 40, 5)}}},
	}}}
	n, _, errOut := run(s, model.Need{VCPU: 1, Provider: "hetzner"})
	if n != 1 || errOut != "" {
		t.Errorf("%d %q", n, errOut)
	}
}

func TestOneTableForAllProvidersWithTheLegendAndTheLinesToCopy(t *testing.T) {
	h := offer("cpx42", "fsn1", 8, 16, 640, 83)
	h.VATPercent = 19
	a := offer("a1.4xlarge", "eu-central-1", 16, 32, 0, 378)
	a.Provider, a.Currency, a.SysDiskGB = "aws", "USD", 400
	z := offer("Standard_B16pls_v2", "germanywestcentral", 16, 32, 0, 383)
	z.Provider, z.SysDiskGB = "azure", 400
	s := find.Service{Deps: find.Deps{Rates: fakeRates{rate: 0.8}, Sources: []find.Source{
		{Name: "aws", Catalog: fakeCatalog{offers: []model.Offer{a}}},
		{Name: "azure", Catalog: fakeCatalog{offers: []model.Offer{z}}},
		{Name: "hetzner", Catalog: fakeCatalog{offers: []model.Offer{h}}},
	}}}
	_, out, _ := run(s, model.Need{VCPU: 8, DiskGB: 400})
	if strings.Count(out, "PROVIDER  TYPE") != 1 {
		t.Errorf("one header:\n%s", out)
	}
	for _, want := range []string{"€", "list prices in EUR, incl. 19 % VAT", "USD converted", "AZURE_ROOT_GB=400", "AWS_ROOT_GB=400"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestTheNotesComeAfterTheTable(t *testing.T) {
	s := find.Service{Deps: find.Deps{Sources: []find.Source{{Name: "aws", Skip: "no credentials"}}}}
	if n, out, errOut := run(s, model.Need{VCPU: 1}); n != 0 || out != "" || !strings.Contains(errOut, "skipped aws") {
		t.Errorf("%d %q %q", n, out, errOut)
	}
}

// fakeRates knows the rate of dollars to euro.
type fakeRates struct {
	rate float64
	err  error
}

func (f fakeRates) Rate(_ context.Context, from, to string) (fx.Rate, error) {
	if f.err != nil || from != "USD" || to != "EUR" {
		return fx.Rate{}, errors.New("no rate")
	}
	return fx.Rate{Value: f.rate, Date: "2026-10-28"}, nil
}

// pricedAlike are a Hetzner type with 19 % VAT in the price (119 euro) and an AWS type of 100 dollars.
func pricedAlike(rates fx.Rates) find.Service {
	h := offer("cpx42", "fsn1", 8, 16, 320, 119)
	h.VATPercent = 19
	a := offer("m7i.2xlarge", "eu-central-1", 8, 32, 0, 100)
	a.Provider, a.Currency = "aws", "USD"
	return find.Service{Deps: find.Deps{Rates: rates, Sources: []find.Source{
		{Name: "aws", Catalog: fakeCatalog{offers: []model.Offer{a}}},
		{Name: "hetzner", Catalog: fakeCatalog{offers: []model.Offer{h}}},
	}}}
}

// prices are the PER MONTH numbers of the rows, in the order shown.
func prices(t *testing.T, out string) []string {
	t.Helper()
	var got []string
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 9 && (f[0] == "aws" || f[0] == "hetzner") {
			got = append(got, f[0]+" "+f[len(f)-2])
		}
	}
	return got
}

func TestThePricesAreOnOneBasis(t *testing.T) {
	// no VAT given: the VAT that Hetzner reports (19 %) for all; 100 dollars are 80 euro, 95.20 with VAT
	_, out, _ := run(pricedAlike(fakeRates{rate: 0.8}), model.Need{VCPU: 8})
	if got := strings.Join(prices(t, out), ", "); got != "aws    95.20, hetzner   119.00" && got != "aws 95.20, hetzner 119.00" {
		t.Errorf("%s\n%s", got, out)
	}
	for _, want := range []string{"list prices in EUR, incl. 19 % VAT", "USD converted at the rate of the European Central Bank of 2026-10-28 (1 USD = 0.8000 EUR)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	// --vat 0: without VAT, Hetzner's price is 100 euro net, AWS 80 euro
	_, out, _ = run(pricedAlike(fakeRates{rate: 0.8}), model.Need{VCPU: 8, HasVAT: true})
	if got := strings.Join(prices(t, out), ", "); got != "aws 80.00, hetzner 100.00" || !strings.Contains(out, "without VAT") {
		t.Errorf("%s\n%s", got, out)
	}
	// --vat 7
	_, out, _ = run(pricedAlike(fakeRates{rate: 0.8}), model.Need{VCPU: 8, HasVAT: true, VATPercent: 7})
	if got := strings.Join(prices(t, out), ", "); got != "aws 85.60, hetzner 107.00" || !strings.Contains(out, "incl. 7 % VAT") {
		t.Errorf("%s\n%s", got, out)
	}
}

func TestWithoutARateTheCurrencyStaysAndIsSaid(t *testing.T) {
	for name, rates := range map[string]fx.Rates{"no rates": nil, "rates fail": fakeRates{err: errors.New("down")}} {
		_, out, _ := run(pricedAlike(rates), model.Need{VCPU: 8})
		if !strings.Contains(out, "NOT converted (no exchange rate): USD") || !strings.Contains(out, "$") {
			t.Errorf("%s:\n%s", name, out)
		}
	}
}

func TestWhatCouldNotBeConvertedIsNeverRankedAgainstEuro(t *testing.T) {
	h := offer("cpx42", "fsn1", 8, 16, 320, 100) // 100 euro a month
	a := offer("m7i.2xlarge", "eu-central-1", 8, 32, 0, 10)
	a.Provider, a.Currency = "aws", "USD" // 10 dollars: a lower number, but no rate to compare
	s := find.Service{Deps: find.Deps{Sources: []find.Source{
		{Name: "aws", Catalog: fakeCatalog{offers: []model.Offer{a}}},
		{Name: "hetzner", Catalog: fakeCatalog{offers: []model.Offer{h}}},
	}}}
	_, out, _ := run(s, model.Need{VCPU: 8})
	if h, a := strings.Index(out, "hetzner"), strings.Index(out, "aws "); h < 0 || a < h {
		t.Errorf("the euro row comes first:\n%s", out)
	}
	for _, want := range []string{"in EUR (except where another currency is shown)", "NOT converted (no exchange rate): USD", "listed after the others"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestTheNumbersAreAlignedToTheRight(t *testing.T) {
	small := offer("a1", "fsn1", 4, 8, 80, 9.5)
	big := offer("b-long-name", "fsn1", 16, 1488, 640, 1015.65)
	s := find.Service{Deps: find.Deps{Sources: []find.Source{{Name: "hetzner", Catalog: fakeCatalog{offers: []model.Offer{small, big}}}}}}
	_, out, _ := run(s, model.Need{VCPU: 1})
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "PROVIDER") || strings.HasPrefix(l, "hetzner") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 3 {
		t.Fatalf("%q", out)
	}
	// the numbers end in the same character column in every line: the header, the cheaper, the dearer row
	end := func(l, word string) int { return strings.Index(l, word) + len(word) }
	if end(lines[0], "vCPU") != end(lines[1], " 4 ")-1 || end(lines[0], "vCPU") != end(lines[2], "16") {
		t.Errorf("vCPU:\n%s", strings.Join(lines, "\n"))
	}
	if n := utf8.RuneCountInString(lines[0]); n != utf8.RuneCountInString(lines[1]) || n != utf8.RuneCountInString(lines[2]) {
		t.Errorf("the last column ends at the same place:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[1], "    9.50 €") || !strings.Contains(lines[2], " 1015.65 €") {
		t.Errorf("the prices end under each other:\n%s", strings.Join(lines, "\n"))
	}
}
