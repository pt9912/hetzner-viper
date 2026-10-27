package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/app"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

func TestUpOpensVSCodeUnlessToldNot(t *testing.T) {
	c := &model.Machine{Provider: "hetzner"}
	if app.Up(c, driving.UpOptions{}).Deps.OpenVSCode == nil {
		t.Error("VS Code is opened by default")
	}
	if app.Up(c, driving.UpOptions{NoCode: true}).Deps.OpenVSCode != nil {
		t.Error("--no-code still opens VS Code")
	}
}

func TestUpWaitsWithTheSleepOfTheCaller(t *testing.T) {
	c := &model.Machine{Provider: "hetzner"}
	slept := 0
	app.Up(c, driving.UpOptions{Sleep: func(time.Duration) { slept++ }}).Deps.Sleep(0)
	if slept != 1 {
		t.Errorf("slept %d times", slept)
	}
}

func TestAProviderWithoutAnAdapterFailsInsteadOfPanicking(t *testing.T) {
	c := &model.Machine{Provider: "nope"}
	if _, err := app.Up(c, driving.UpOptions{}).Deps.Cloud.ServerByName(context.Background(), "x"); err == nil {
		t.Error("want an error")
	}
	if f := app.Preflight(c).Deps.Cloud.Validate(context.Background(), model.Check{}); len(f) != 1 || f[0].Level != model.Fail {
		t.Errorf("findings %+v", f)
	}
}
