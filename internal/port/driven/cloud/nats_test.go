package cloud_test

import (
	"testing"

	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
	"github.com/pt9912/hetzner-viper/internal/servertest"
)

// wrapper is a provider held by a wrapper, like internal/app.Provider.
type wrapper struct{ cloud.Provider }

func (w wrapper) Unwrap() any {
	if w.Provider == nil {
		return nil
	}
	return w.Provider
}

type plain struct{ cloud.Provider }

func TestAsNATSLooksThroughWrappers(t *testing.T) {
	var n cloud.NATSProvider
	if !cloud.AsNATS(&servertest.Cloud{}, &n) || n == nil {
		t.Error("the fake can do NATS")
	}
	if !cloud.AsNATS(wrapper{&servertest.Cloud{}}, &n) {
		t.Error("through the wrapper")
	}
	for name, p := range map[string]any{"plain": wrapper{plain{}}, "empty wrapper": wrapper{}, "nil": nil} {
		var m cloud.NATSProvider
		if cloud.AsNATS(p, &m) || m != nil {
			t.Errorf("%s cannot", name)
		}
	}
}
