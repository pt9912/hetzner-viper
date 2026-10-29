// Package fx is the driven port to exchange rates, so that the prices of providers that bill in
// different currencies can be compared.
package fx

import "context"

// Rate says how much of a currency one unit of another is worth.
type Rate struct {
	Value float64 // one unit of the first currency is Value of the second
	Date  string  // the day the rate is from, e.g. 2026-10-28
}

// Rates are the exchange rates.
type Rates interface {
	// Rate is the rate from one currency to another (ISO codes such as "USD", "EUR").
	Rate(ctx context.Context, from, to string) (Rate, error)
}
