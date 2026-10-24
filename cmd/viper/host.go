package main

import (
	"os/exec"

	"github.com/pt9912/hetzner-viper/internal/check"
	"github.com/pt9912/hetzner-viper/internal/config"
	"github.com/pt9912/hetzner-viper/internal/preflight"
	"github.com/pt9912/hetzner-viper/internal/start"
	"github.com/pt9912/hetzner-viper/internal/stop"
)

// host is what the commands use of the outside world: the programs they run and the dependencies
// of the internal packages. The tests replace them.
type host struct {
	runTool          func(name string, args ...string) error
	execTool         func(name string, args ...string) error
	lookPath         func(file string) (string, error)
	newCheckDeps     func(c *config.Config) check.Deps
	newDownDeps      func(c *config.Config) stop.Deps
	newPreflightDeps func(c *config.Config) preflight.Deps
	newUpDeps        func(c *config.Config, preflightDeps func() preflight.Deps) start.Deps
}

func defaultHost() *host {
	return &host{
		runTool:          runChild,
		execTool:         replaceProcess,
		lookPath:         exec.LookPath,
		newCheckDeps:     defaultCheckDeps,
		newDownDeps:      defaultDownDeps,
		newPreflightDeps: defaultPreflightDeps,
		newUpDeps:        defaultUpDeps,
	}
}
