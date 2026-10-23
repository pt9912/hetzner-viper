package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/pt9912/hetzner-viper/internal/cloudflare"
	"github.com/pt9912/hetzner-viper/internal/config"
	"github.com/pt9912/hetzner-viper/internal/hetzner"
	"github.com/pt9912/hetzner-viper/internal/preflight"
	"github.com/pt9912/hetzner-viper/internal/runlog"
)

// exitStatus ends viper with that code; the reason was already shown.
type exitStatus int

func (e exitStatus) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// replaced by the tests
var newPreflightDeps = func(c *config.Config) preflight.Deps {
	d := preflight.Deps{
		Hetzner: hetzner.New(c.Get("HCLOUD_TOKEN"), ""),
		DNS:     &cloudflare.Client{Token: c.Get("CLOUDFLARE_API_TOKEN"), ZoneID: c.Get("CLOUDFLARE_ZONE_ID")},
	}
	if _, err := exec.LookPath("git"); err == nil {
		d.GitLsRemote = func(ctx context.Context, url string) error {
			cmd := exec.CommandContext(ctx, "git", "ls-remote", "--", url)
			cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
			return cmd.Run()
		}
	}
	return d
}

// runPreflight is preflight.sh: the checks, their output also in the machine's log, the outcome
// in events.log.
func runPreflight(ctx context.Context, o *options, env config.Env, stdout, stderr io.Writer) error {
	c, err := config.Load(settingsDir(o), o.machine, env, true)
	if err != nil {
		return err
	}
	l, err := runlog.Open(c.StateDir, c.ServerName, "preflight", stdout, stderr)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	failures := preflight.Run(ctx, c, o.repo, newPreflightDeps(c), l.Out, l.Err)
	switch {
	case ctx.Err() != nil:
		l.Interrupted()
		l.Close(ctx.Err(), 130)
		return exitStatus(130)
	case failures > 0:
		l.Final("preflight: %d problem(s)", failures)
		l.Close(nil, 1)
		return exitStatus(1)
	}
	l.Final("preflight OK")
	l.Close(nil, 0)
	return nil
}

func nativePreflight(o *options) error {
	env, err := hostEnv()
	if err != nil {
		return err
	}
	return runPreflight(context.Background(), o, env, os.Stdout, os.Stderr)
}
