package livecheck_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/livecheck"
)

func TestWhatIsNotAFinding(t *testing.T) {
	for name, err := range map[string]error{
		"timeout":      fmt.Errorf("pricing page: %w", context.DeadlineExceeded),
		"dns":          fmt.Errorf("pricing page: %w", &net.DNSError{Err: "no such host", Name: "x"}),
		"broke off":    fmt.Errorf("price list: %w", io.ErrUnexpectedEOF),
		"blocked":      errors.New("pricing page: HTTP 403"),
		"rate limited": errors.New("price list: HTTP 429"),
		"outage":       errors.New("pricing page: HTTP 503"),
	} {
		if !livecheck.Inconclusive(err) {
			t.Errorf("%s: want inconclusive", name)
		}
	}
}

func TestWhatIsAFinding(t *testing.T) {
	for name, err := range map[string]error{
		"nothing":      nil,
		"changed":      errors.New("no prices found on the pricing page cloud/general-purpose/ (did it change?)"),
		"gone":         errors.New("pricing page: HTTP 404"),
		"format":       errors.New("pricing data: invalid character '<' looking for beginning of value"),
		"no fee found": errors.New("no fee for an external IPv4 address found on the network pricing page (did it change?)"),
	} {
		if livecheck.Inconclusive(err) {
			t.Errorf("%s: want a finding", name)
		}
	}
}
