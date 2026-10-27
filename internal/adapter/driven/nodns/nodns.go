// Package nodns is the adapter of the DNS port (internal/port/driven/dns) for a machine without a
// DNS name (DNS_PROVIDER=none): it makes no record, removes none and finds nothing to check.
package nodns

import (
	"context"
	"fmt"
)

// Client is the DNS service that does nothing.
type Client struct{}

// Set makes no record; it says where the server is instead.
func (Client) Set(_ context.Context, _, ip string) (string, error) {
	return fmt.Sprintf("No DNS record (DNS_PROVIDER=none): the server is at %s; connect with viper ssh <machine>.", ip), nil
}

// Delete has no record to remove.
func (Client) Delete(context.Context, string, string) (string, error) {
	return "No DNS record to remove (DNS_PROVIDER=none).", nil
}

// Check has nothing to read.
func (Client) Check(context.Context, string, string) (lines []string, ok bool, err error) {
	return []string{"ok    DNS_PROVIDER=none: no DNS record is made, the server is reached by its address"}, true, nil
}
