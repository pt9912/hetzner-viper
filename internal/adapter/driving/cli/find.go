package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

// runFind shows the server types that fit the need, of every provider whose credentials are set.
func runFind(ctx context.Context, o *options, env settings.Env, stdout, stderr io.Writer) error {
	if o.cpu == 0 && o.ram == 0 && o.disk == 0 {
		return fmt.Errorf("viper find: give at least one of --cpu, --ram, --disk\n%s", usageLine("find"))
	}
	if o.arch != "" && o.arch != "x86" && o.arch != "arm" {
		return fmt.Errorf("viper find: --arch is x86 or arm: %s", o.arch)
	}
	if _, ok := model.LookupProvider(o.provider); o.provider != "" && !ok {
		return fmt.Errorf("viper find: --provider is one of %s: %s", strings.Join(model.ProviderNames(), ", "), o.provider)
	}
	vars, err := o.host().Services.Settings.LoadFile(settingsDir(o), env)
	if err != nil {
		return err
	}
	get := func(k string) string {
		if v, ok := vars[k]; ok {
			return v
		}
		return env.Getenv(k)
	}
	ctx, stop := notifyContext(ctx)
	defer stop()
	need := model.Need{VCPU: o.cpu, RAMGB: o.ram, DiskGB: o.disk, Arch: o.arch, Location: o.location, Provider: o.provider}
	if o.host().Services.Find(get, stderr).Find(ctx, need, stdout, stderr) == 0 {
		if ctx.Err() != nil {
			return exitStatus(130)
		}
		return exitStatus(1)
	}
	return nil
}

func nativeFind(o *options) error {
	env, err := o.host().Env()
	if err != nil {
		return err
	}
	return runFind(context.Background(), o, env, o.host().Stdout, o.host().Stderr)
}
