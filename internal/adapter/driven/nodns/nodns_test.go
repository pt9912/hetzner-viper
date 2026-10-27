package nodns_test

import (
	"context"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/nodns"
	"github.com/pt9912/hetzner-viper/internal/port/driven/dns"
)

func TestItMakesAndRemovesNothing(t *testing.T) {
	var d dns.Provider = nodns.Client{}
	ctx := context.Background()
	msg, err := d.Set(ctx, "claude-web.viper.invalid", "203.0.113.7")
	if err != nil || !strings.Contains(msg, "203.0.113.7") || !strings.Contains(msg, "DNS_PROVIDER=none") {
		t.Errorf("Set: %q %v", msg, err)
	}
	if msg, err := d.Delete(ctx, "claude-web.viper.invalid", "203.0.113.7"); err != nil || msg == "" {
		t.Errorf("Delete: %q %v", msg, err)
	}
	lines, ok, err := d.Check(ctx, "viper.invalid", "claude-web.viper.invalid")
	if err != nil || !ok || len(lines) != 1 || !strings.HasPrefix(lines[0], "ok ") {
		t.Errorf("Check: %q %v %v", lines, ok, err)
	}
}
