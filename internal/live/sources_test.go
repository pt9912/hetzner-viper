//go:build live

package live_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/aws"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/azure"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/ecb"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/gcp"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/hetzner"
	"github.com/pt9912/hetzner-viper/internal/livecheck"
	"github.com/pt9912/hetzner-viper/internal/model"
)

// offers reads the offers of a source. A source that cannot be reached skips the check; one that
// answers but cannot be read any more fails it, with what the catalog says.
func offers(t *testing.T, read func(ctx context.Context) ([]model.Offer, error)) []model.Offer {
	t.Helper()
	t.Parallel() // the sources are independent, and one slow one must not use up the time of the others
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	out, err := read(ctx)
	switch {
	case err == nil:
		return out
	case livecheck.Inconclusive(err):
		t.Skipf("the source cannot be reached: %v", err)
	default:
		t.Fatalf("the source cannot be read any more: %v", err)
	}
	return nil
}

// has reports whether an offer fits the test.
func has(offers []model.Offer, fits func(model.Offer) bool) bool {
	for _, o := range offers {
		if fits(o) {
			return true
		}
	}
	return false
}

// sane checks what every offer of every source must be: a price, a size, a currency and the fee for the
// IPv4 address.
func sane(t *testing.T, offers []model.Offer) {
	t.Helper()
	for _, o := range offers {
		if o.HourlyPrice <= 0 || o.MonthlyPrice <= 0 || o.VCPU <= 0 || o.RAMGB <= 0 || o.Currency == "" || !o.IPv4 {
			t.Fatalf("an offer that is not whole: %+v", o)
		}
	}
}

func TestHetznerPages(t *testing.T) {
	all := offers(t, func(ctx context.Context) ([]model.Offer, error) {
		return hetzner.NewPublic(t.TempDir(), nil).Offers(ctx, "", 0)
	})
	if len(all) < 20 {
		t.Errorf("only %d offers", len(all))
	}
	sane(t, all)
	// the families the pages show: a type may be retired, a family not unnoticed
	for _, family := range []string{"cpx", "ccx"} {
		if !has(all, func(o model.Offer) bool { return strings.HasPrefix(o.Type, family) && o.DiskGB > 0 }) {
			t.Errorf("no type of the family %s is among the offers", family)
		}
	}
	if !has(all, func(o model.Offer) bool { return o.Location == "fsn1" }) || !has(all, func(o model.Offer) bool { return o.Location == "hel1" }) {
		t.Error("fsn1 or hel1 is missing")
	}
}

func TestGooglePages(t *testing.T) {
	all := offers(t, func(ctx context.Context) ([]model.Offer, error) {
		return gcp.NewCatalog("", t.TempDir(), nil).Offers(ctx, "europe-west3-b", 50)
	})
	if len(all) < 100 {
		t.Errorf("only %d offers", len(all))
	}
	sane(t, all)
	for _, family := range []string{"e2-", "n2-"} {
		if !has(all, func(o model.Offer) bool { return strings.HasPrefix(o.Type, family) && o.VCPU > 0 && o.RAMGB > 0 }) {
			t.Errorf("no type of the family %s is among the offers", family)
		}
	}
	for _, o := range all {
		if o.Type == "e2-micro" || o.Type == "e2-small" || o.Type == "e2-medium" {
			t.Errorf("a shared core is among the offers: %s", o.Type)
		}
	}
}

func TestAzureLists(t *testing.T) {
	all := offers(t, func(ctx context.Context) ([]model.Offer, error) {
		return azure.NewCatalog("", t.TempDir(), nil).Offers(ctx, "germanywestcentral", 64)
	})
	if len(all) < 50 {
		t.Errorf("only %d offers", len(all))
	}
	sane(t, all)
	// sizes from the website and prices from the retail API have met: some size with 4 vCPUs and 16 GB
	if !has(all, func(o model.Offer) bool { return o.VCPU == 4 && o.RAMGB == 16 }) {
		t.Error("no size with 4 vCPUs and 16 GB is among the offers")
	}
}

func TestAWSPriceList(t *testing.T) {
	// a small region: the format of the list is the same in all of them
	all := offers(t, func(ctx context.Context) ([]model.Offer, error) {
		return aws.NewCatalog(t.TempDir(), nil).Offers(ctx, "mx-central-1", 50)
	})
	if len(all) < 30 {
		t.Errorf("only %d offers", len(all))
	}
	sane(t, all)
	if !has(all, func(o model.Offer) bool { return o.Arch == "arm" }) || !has(all, func(o model.Offer) bool { return o.Arch == "x86" }) {
		t.Error("types of both architectures are expected")
	}
}

func TestECBRates(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	r, err := ecb.New(t.TempDir()).Rate(ctx, "USD", "EUR")
	switch {
	case err != nil && livecheck.Inconclusive(err):
		t.Skipf("the source cannot be reached: %v", err)
	case err != nil:
		t.Fatalf("the rates cannot be read any more: %v", err)
	case r.Value < 0.3 || r.Value > 2 || r.Date == "":
		t.Errorf("a rate that does not look like one: %+v", r)
	}
}
