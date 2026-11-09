package app_test

import (
	"testing"

	"github.com/pt9912/hetzner-viper/internal/app"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

func TestEveryConfiguredProviderHasAnAdapter(t *testing.T) {
	for _, name := range model.ProviderNames() {
		if _, ok := model.LookupProvider(name); !ok {
			t.Errorf("%s: config does not know it", name)
		}
		if p := app.NewProvider(&model.Machine{Provider: name}); p.Provider == nil || p.Name() == "" {
			t.Errorf("%s: no adapter, or one without a name", name)
		}
	}
	if app.NewProvider(&model.Machine{Provider: "nope"}).Provider != nil {
		t.Error("an unknown provider has no adapter")
	}
}

func TestOnlyHetznerCanHoldNATSGroupsForNow(t *testing.T) {
	for _, name := range model.ProviderNames() {
		var n cloud.NATSProvider
		ok := cloud.AsNATS(app.NewProvider(&model.Machine{Provider: name}), &n)
		if ok != (name == "hetzner") {
			t.Errorf("%s: NATS capability %v", name, ok)
		}
	}
}
