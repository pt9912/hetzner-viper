package integration_test

import (
	"strings"
	"testing"
)

func TestCheckReportsSwap(t *testing.T) {
	e := New(t, "VIPER_SWAP=1\n")
	e.MustUp("web", "")
	r := e.Check("web")
	if r.Code != 0 {
		t.Fatalf("%d\n%s", r.Code, r.Out)
	}
	r.Has(t, "ok    swap active (8192 MB)")
	// the swap is gone: the check says so
	e.Srv.Write("swap-active", "")
	r = e.Check("web")
	if r.Code != 1 {
		t.Errorf("code %d", r.Code)
	}
	r.Has(t, "FAIL  swap not active")
	if !strings.Contains(e.Events(), "check: 1 problem(s)") {
		t.Errorf("events %s", e.Events())
	}
}
