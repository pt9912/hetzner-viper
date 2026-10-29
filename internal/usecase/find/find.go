// Package find looks up the server types of the providers that fit what a machine needs and shows
// them with their prices. It only reads and changes nothing.
package find

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
	"text/tabwriter"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/catalog"
)

// DefaultTop is how many server types of each provider are shown.
const DefaultTop = 3

// Source is the price list of one provider; Catalog is nil if there is none to read, and Skip
// says why.
type Source struct {
	Name    string
	Catalog catalog.Catalog
	Skip    string
	// Location is where to look if the need names none ("" for all locations at once).
	Location string
	// Describe words a failure of the catalog for the user.
	Describe func(error) string
}

// Deps are the outside world of the search.
type Deps struct {
	Sources []Source
	Top     int // per provider; 0 is DefaultTop
}

// Service is the use case Find.
type Service struct{ Deps }

// result is what one provider gave: the server types to show, or a line for errOut.
type result struct {
	best []model.Offer
	note string
}

// Find shows the cheapest server types of every provider that have at least what need asks for
// and returns how many it showed. The price lists are read at the same time; the output keeps the
// order of the providers. A provider that cannot be read is named on errOut and left out.
func (s Service) Find(ctx context.Context, need model.Need, out, errOut io.Writer) int {
	top := s.Top
	if top <= 0 {
		top = DefaultTop
	}
	results := make([]result, len(s.Sources))
	var wg sync.WaitGroup
	for i, src := range s.Sources {
		if need.Provider != "" && need.Provider != src.Name {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = look(ctx, src, need, top)
		}()
	}
	wg.Wait()
	shown := 0
	for i, r := range results {
		if r.note != "" {
			fmt.Fprintln(errOut, r.note)
		}
		if len(r.best) > 0 {
			show(out, s.Sources[i].Name, r.best, need)
			shown += len(r.best)
		}
	}
	return shown
}

// look reads the price list of one provider and picks the cheapest that fit.
func look(ctx context.Context, src Source, need model.Need, top int) result {
	if src.Catalog == nil {
		return result{note: fmt.Sprintf("skipped %s: %s", src.Name, src.Skip)}
	}
	location := need.Location
	if location == "" {
		location = src.Location
	}
	offers, err := src.Catalog.Offers(ctx, location, need.DiskGB)
	if err != nil {
		reason := err.Error()
		if src.Describe != nil {
			reason = src.Describe(err)
		}
		return result{note: fmt.Sprintf("skipped %s: %s", src.Name, reason)}
	}
	best := cheapest(offers, need, top)
	if len(best) == 0 {
		return result{note: fmt.Sprintf("%s: no server type fits", src.Name)}
	}
	return result{best: best}
}

// cheapest are the top cheapest server types that fit, each in the location where it is cheapest.
func cheapest(offers []model.Offer, need model.Need, top int) []model.Offer {
	byType := map[string]model.Offer{}
	for _, o := range offers {
		if !o.Fits(need) {
			continue
		}
		if cur, ok := byType[o.Type]; !ok || less(o, cur) {
			byType[o.Type] = o
		}
	}
	fit := make([]model.Offer, 0, len(byType))
	for _, o := range byType {
		fit = append(fit, o)
	}
	sort.Slice(fit, func(i, j int) bool { return less(fit[i], fit[j]) })
	if len(fit) > top {
		fit = fit[:top]
	}
	return fit
}

// less orders by price, then by name, so that the result does not depend on the order of the API.
func less(a, b model.Offer) bool {
	if a.MonthlyPrice != b.MonthlyPrice {
		return a.MonthlyPrice < b.MonthlyPrice
	}
	if a.Type != b.Type {
		return a.Type < b.Type
	}
	return a.Location < b.Location
}

func show(out io.Writer, provider string, best []model.Offer, need model.Need) {
	vat := "without VAT"
	if best[0].VATPercent != 0 {
		vat = fmt.Sprintf("incl. %g %% VAT", best[0].VATPercent)
	}
	fmt.Fprintf(out, "== %s (list prices in %s, %s)\n", provider, best[0].Currency, vat)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TYPE\tLOCATION\tvCPU\tRAM GB\tDISK GB\tARCH\tPER HOUR\tPER MONTH")
	for _, o := range best {
		disk := "-"
		switch {
		case o.DiskGB != 0:
			disk = fmt.Sprint(o.DiskGB)
		case o.SysDiskGB != 0:
			disk = fmt.Sprintf("%d*", o.SysDiskGB)
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%g\t%s\t%s\t%.4f\t%.2f\n", o.Type, o.Location, o.VCPU, o.RAMGB, disk, o.Arch, o.HourlyPrice, o.MonthlyPrice)
	}
	_ = w.Flush()
	c := best[0]
	if c.SysDiskGB != 0 {
		fmt.Fprintln(out, "* system disk, chosen apart from the type; its price is in the prices")
	}
	fmt.Fprintf(out, "cheapest: PROVIDER=%s SERVER_TYPE=%s SERVER_LOCATION=%s", c.Provider, c.Type, c.Location)
	if info, _ := model.LookupProvider(c.Provider); c.DiskGB == 0 && info.DiskSetting != "" && need.DiskGB > 0 {
		fmt.Fprintf(out, " %s=%d", info.DiskSetting, need.DiskGB)
	}
	fmt.Fprintln(out)
}
