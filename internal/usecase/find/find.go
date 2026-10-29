// Package find looks up the server types of the providers that fit what a machine needs and shows
// them with their prices, on one basis so that they can be compared: one currency (euro) and the
// same VAT for all. It only reads and changes nothing.
package find

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/catalog"
	"github.com/pt9912/hetzner-viper/internal/port/driven/fx"
)

// DefaultTop is how many server types of each provider are shown.
const DefaultTop = 3

// currency is the currency all prices are shown in.
const currency = "EUR"

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
	// Rates converts the prices of a provider that bills in another currency; nil to show them as
	// they are.
	Rates fx.Rates
}

// Service is the use case Find.
type Service struct{ Deps }

// result is what one provider gave: all its offers, or a line for errOut.
type result struct {
	offers []model.Offer
	read   bool // the price list was read (it may have no offers)
	note   string
}

// Find shows the cheapest server types of every provider that have at least what need asks for
// and returns how many it showed. The price lists are read at the same time. The prices are
// brought to one basis (euro, one VAT) and the rows are ordered by price. A provider that cannot be
// read is named on errOut and left out.
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
			results[i] = look(ctx, src, need)
		}()
	}
	wg.Wait()
	basis := s.normalize(ctx, need, results)
	var rows []model.Offer
	for i, r := range results {
		best := cheapest(r.offers, need, top)
		if len(best) == 0 && r.read {
			results[i].note = fmt.Sprintf("%s: no server type fits", s.Sources[i].Name)
		}
		rows = append(rows, best...)
	}
	sort.Slice(rows, func(i, j int) bool { return less(rows[i], rows[j]) })
	show(out, rows, basis, need)
	// the notes after the table, so that they are not lost between it and the command
	for _, r := range results {
		if r.note != "" {
			fmt.Fprintln(errOut, r.note)
		}
	}
	return len(rows)
}

// look reads the price list of one provider.
func look(ctx context.Context, src Source, need model.Need) result {
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
	return result{offers: offers, read: true}
}

// basis says what the prices were brought to.
type basis struct {
	vat       float64            // the VAT in all prices; 0 for none
	converted map[string]fx.Rate // by currency that was converted to euro
	kept      []string           // currencies that could not be converted
}

// normalize brings all the offers to euro and to one VAT, in place: --vat if it is given, else the
// VAT that a provider reports (Hetzner, whose prices have it in), else none.
func (s Service) normalize(ctx context.Context, need model.Need, results []result) basis {
	b := basis{converted: map[string]fx.Rate{}, vat: need.VATPercent}
	if !need.HasVAT {
		b.vat = reportedVAT(results)
	}
	failed := map[string]bool{}
	for i := range results {
		for j := range results[i].offers {
			o := &results[i].offers[j]
			factor := (1 + b.vat/100) / (1 + o.VATPercent/100) // from the VAT in the price to the VAT asked for
			if o.Currency != currency {
				if rate, ok := s.rate(ctx, o.Currency, &b, failed); ok {
					factor *= rate
					o.Currency = currency
				}
			}
			o.HourlyPrice *= factor
			o.MonthlyPrice *= factor
			o.VATPercent = b.vat
		}
	}
	for c := range failed {
		b.kept = append(b.kept, c)
	}
	sort.Strings(b.kept)
	return b
}

// reportedVAT is the VAT that the prices of a provider have in, 0 if none has any.
func reportedVAT(results []result) float64 {
	for _, r := range results {
		for _, o := range r.offers {
			if o.VATPercent != 0 {
				return o.VATPercent
			}
		}
	}
	return 0
}

// rate is the rate to euro of a currency, asked once.
func (s Service) rate(ctx context.Context, from string, b *basis, failed map[string]bool) (float64, bool) {
	if r, ok := b.converted[from]; ok {
		return r.Value, true
	}
	if failed[from] || s.Rates == nil {
		failed[from] = true
		return 0, false
	}
	r, err := s.Rates.Rate(ctx, from, currency)
	if err != nil {
		failed[from] = true
		return 0, false
	}
	b.converted[from] = r
	return r.Value, true
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
	if a.Provider != b.Provider {
		return a.Provider < b.Provider
	}
	if a.Type != b.Type {
		return a.Type < b.Type
	}
	return a.Location < b.Location
}

// show prints one table of the rows, what the prices mean, and the lines to copy.
func show(out io.Writer, rows []model.Offer, b basis, need model.Need) {
	if len(rows) == 0 {
		return
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROVIDER\tTYPE\tLOCATION\tvCPU\tRAM GB\tDISK GB\tARCH\tPER HOUR\tPER MONTH")
	sysDisk := false
	for _, o := range rows {
		disk := "-"
		switch {
		case o.DiskGB != 0:
			disk = fmt.Sprint(o.DiskGB)
		case o.SysDiskGB != 0:
			disk, sysDisk = fmt.Sprintf("%d*", o.SysDiskGB), true
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%g\t%s\t%s\t%s\t%s\n", o.Provider, o.Type, o.Location, o.VCPU, o.RAMGB, disk, o.Arch,
			money(o.HourlyPrice, 4, o.Currency), money(o.MonthlyPrice, 2, o.Currency))
	}
	_ = w.Flush()
	fmt.Fprintln(out, "\n"+legend(b))
	if sysDisk {
		fmt.Fprintln(out, "* system disk, chosen apart from the type; its price is in the prices.")
	}
	fmt.Fprintln(out, "\nFor machines/<machine>.env.local (the cheapest of each provider):")
	seen := map[string]bool{}
	for _, o := range rows {
		if !seen[o.Provider] {
			seen[o.Provider] = true
			fmt.Fprintf(out, "  %s\n", settingsLine(o, need))
		}
	}
}

// legend says what the prices are: list prices on one basis, and where they were converted.
func legend(b basis) string {
	vat := "without VAT"
	if b.vat != 0 {
		vat = fmt.Sprintf("incl. %g %% VAT", b.vat)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Prices are list prices in %s, %s; the fee for a public IPv4 address is not in them.", currency, vat)
	var from []string
	for c := range b.converted {
		from = append(from, c)
	}
	sort.Strings(from)
	for _, c := range from {
		r := b.converted[c]
		fmt.Fprintf(&sb, "\n%s converted at the rate of the European Central Bank of %s (1 %s = %.4f %s).", c, r.Date, c, r.Value, currency)
	}
	if len(b.kept) > 0 {
		fmt.Fprintf(&sb, "\nNOT converted (no exchange rate): %s; those prices cannot be compared with the others.", strings.Join(b.kept, ", "))
	}
	return sb.String()
}

// money is a price with its currency, aligned to the right.
func money(v float64, decimals int, currency string) string {
	symbol := currency
	switch currency {
	case "EUR":
		symbol = "€"
	case "USD":
		symbol = "$"
	}
	return fmt.Sprintf("%*.*f %s", 9, decimals, v, symbol)
}

// settingsLine is the settings that make a machine of the offer.
func settingsLine(o model.Offer, need model.Need) string {
	line := fmt.Sprintf("PROVIDER=%s SERVER_TYPE=%s SERVER_LOCATION=%s", o.Provider, o.Type, o.Location)
	if info, _ := model.LookupProvider(o.Provider); o.DiskGB == 0 && info.DiskSetting != "" && need.DiskGB > 0 {
		line += fmt.Sprintf(" %s=%d", info.DiskSetting, need.DiskGB)
	}
	return line
}
