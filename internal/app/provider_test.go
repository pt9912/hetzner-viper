package app_test

import (
	"testing"

	"github.com/pt9912/hetzner-viper/internal/app"

	"github.com/pt9912/hetzner-viper/internal/config"
)

func TestEveryConfiguredProviderHasAnAdapter(t *testing.T) {
	for _, name := range config.ProviderNames() {
		if _, ok := config.Provider(name); !ok {
			t.Errorf("%s: config does not know it", name)
		}
		if p := app.NewProvider(name, func(string) string { return "" }); p == nil || p.Name() == "" {
			t.Errorf("%s: no adapter, or one without a name", name)
		}
	}
	if app.NewProvider("nope", func(string) string { return "" }) != nil {
		t.Error("an unknown provider has no adapter")
	}
}
