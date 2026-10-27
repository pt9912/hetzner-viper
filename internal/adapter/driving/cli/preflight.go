package cli

import (
	"context"
	"fmt"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
	"io"
)

// exitStatus ends viper with that code; the reason was already shown.
type exitStatus int

func (e exitStatus) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// runPreflight is preflight.sh: the checks, their output also in the machine's log, the outcome
// in events.log.
func runPreflight(ctx context.Context, o *options, env settings.Env, stdout, stderr io.Writer) error {
	c, err := o.host().Services.Settings.Load(settingsDir(o), o.machine, env, true)
	if err != nil {
		return err
	}
	l, err := o.host().Services.OpenLog(c, "preflight", stdout, stderr)
	if err != nil {
		return err
	}
	ctx, stop := notifyContext(ctx)
	defer stop()
	failures := o.host().Services.Preflight(c).Preflight(ctx, c, o.repo, l.Out(), l.Err())
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
	env, err := o.host().Env()
	if err != nil {
		return err
	}
	return runPreflight(context.Background(), o, env, o.host().Stdout, o.host().Stderr)
}
