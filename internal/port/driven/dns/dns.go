// Package dns is the port to the DNS service that holds the name of a machine (an A record for
// people and other clients; the scripts connect by IP). An adapter (internal/adapter/driven/cloudflare)
// implements it.
package dns

import "context"

// Setter creates or updates the record of a host.
type Setter interface {
	Set(ctx context.Context, host, ip string) (string, error)
}

// Deleter removes the record of a host if it still points to expectedIP.
type Deleter interface {
	Delete(ctx context.Context, host, expectedIP string) (string, error)
}

// Checker reads the zone and the record without changing them.
type Checker interface {
	Check(ctx context.Context, domain, host string) (lines []string, ok bool, err error)
}

// Provider is a DNS service as a whole.
type Provider interface {
	Setter
	Deleter
	Checker
}
