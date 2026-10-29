package find_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/usecase/find"
)

type fakeCatalog struct {
	offers []model.Offer
	err    error
}

func (f fakeCatalog) Offers(context.Context) ([]model.Offer, error) { return f.offers, f.err }

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
	if !strings.Contains(out, "mid   hel1") || strings.Contains(out, "mid   fsn1") {
		t.Errorf("mid should be shown in hel1 only:\n%s", out)
	}
	if !strings.Contains(out, "cheapest: PROVIDER=hetzner SERVER_TYPE=mid SERVER_LOCATION=hel1\n") {
		t.Errorf("no line to copy:\n%s", out)
	}
}

func TestDiskChosenApartIsSetAsASetting(t *testing.T) {
	o := offer("m7i.xlarge", "eu-central-1", 4, 16, 0, 100)
	o.Provider = "aws"
	_, out, _ := run(find.Service{Deps: find.Deps{Sources: []find.Source{{Name: "aws", Catalog: fakeCatalog{offers: []model.Offer{o}}}}}}, model.Need{VCPU: 4, DiskGB: 80})
	if !strings.Contains(out, "SERVER_LOCATION=eu-central-1 AWS_ROOT_GB=80\n") {
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
