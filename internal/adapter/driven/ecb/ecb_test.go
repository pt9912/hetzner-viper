package ecb_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/ecb"
)

const file = `<?xml version="1.0" encoding="UTF-8"?>
<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref">
 <Cube><Cube time='2026-10-28'><Cube currency='USD' rate='1.25'/><Cube currency='GBP' rate='0.5'/></Cube></Cube>
</gesmes:Envelope>`

func server(t *testing.T, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		if status != 200 {
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write([]byte(file))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestRatesOverTheEuro(t *testing.T) {
	srv, _ := server(t, 200)
	c := ecb.NewWith(srv.URL, srv.Client(), "", time.Now)
	for _, tc := range []struct {
		from, to string
		want     float64
	}{{"USD", "EUR", 0.8}, {"EUR", "USD", 1.25}, {"EUR", "EUR", 1}, {"USD", "GBP", 0.4}} {
		r, err := c.Rate(context.Background(), tc.from, tc.to)
		if err != nil || r.Value < tc.want-1e-9 || r.Value > tc.want+1e-9 || r.Date != "2026-10-28" {
			t.Errorf("%s to %s: %+v %v", tc.from, tc.to, r, err)
		}
	}
	if _, err := c.Rate(context.Background(), "XXX", "EUR"); err == nil {
		t.Error("unknown currency: want an error")
	}
}

func TestTheRatesAreReadOnceADayAndOldOnesBeatNone(t *testing.T) {
	srv, requests := server(t, 200)
	dir := t.TempDir()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	c := ecb.NewWith(srv.URL, srv.Client(), dir, func() time.Time { return now })
	rate := func() error { _, err := c.Rate(context.Background(), "USD", "EUR"); return err }
	if err := rate(); err != nil || requests.Load() != 1 {
		t.Fatalf("%v %d", err, requests.Load())
	}
	now = now.Add(23 * time.Hour)
	if err := rate(); err != nil || requests.Load() != 1 {
		t.Errorf("within a day: %v %d", err, requests.Load())
	}
	now = now.Add(2 * time.Hour)
	if err := rate(); err != nil || requests.Load() != 2 {
		t.Errorf("after a day: %v %d", err, requests.Load())
	}
	srv.Close()
	now = now.Add(72 * time.Hour)
	if err := rate(); err != nil {
		t.Errorf("the file cannot be read: %v", err)
	}
}

func TestAFailureWithoutOldRatesIsAnError(t *testing.T) {
	srv, _ := server(t, 503)
	_, err := ecb.NewWith(srv.URL, srv.Client(), "", time.Now).Rate(context.Background(), "USD", "EUR")
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Errorf("%v", err)
	}
}

func TestRatesOlderThanAWeekAreNotUsedWhenTheFileCannotBeRead(t *testing.T) {
	srv, _ := server(t, 200)
	dir := t.TempDir()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	c := ecb.NewWith(srv.URL, srv.Client(), dir, func() time.Time { return now })
	if _, err := c.Rate(context.Background(), "USD", "EUR"); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	now = now.Add(5 * 24 * time.Hour)
	if _, err := c.Rate(context.Background(), "USD", "EUR"); err != nil {
		t.Errorf("five days old: %v", err)
	}
	now = now.Add(5 * 24 * time.Hour)
	if _, err := c.Rate(context.Background(), "USD", "EUR"); err == nil {
		t.Error("ten days old: want an error")
	}
}
