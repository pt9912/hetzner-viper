package cli

import (
	"context"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
	"io"
)

// runCheck is check.sh: read-only checks of a running machine, logged like the script's.
func runCheck(ctx context.Context, o *options, env settings.Env, stdout, stderr io.Writer) error {
	c, err := o.host().Services.Settings.Load(settingsDir(o), o.machine, env, true)
	if err != nil {
		return err
	}
	l, err := o.host().Services.OpenLog(c, "check", stdout, stderr)
	if err != nil {
		return err
	}
	ctx, stop := notifyContext(ctx)
	defer stop()
	failures, err := o.host().Services.Check(c).Check(ctx, c, l.Out(), l.Err())
	switch {
	case ctx.Err() != nil:
		l.Interrupted()
		l.Close(ctx.Err(), 130)
		return exitStatus(130)
	case err != nil:
		// the reason was shown; without a final event the run is logged as FAILED
		l.Close(err, 1)
		return exitStatus(1)
	case failures > 0:
		l.Final("check: %d problem(s)", failures)
		l.Close(nil, 1)
		return exitStatus(1)
	}
	l.Final("check OK")
	l.Close(nil, 0)
	return nil
}

func nativeCheck(o *options) error {
	if err := o.host().need("ssh"); err != nil {
		return err
	}
	env, err := o.host().Env()
	if err != nil {
		return err
	}
	return runCheck(context.Background(), o, env, o.host().Stdout, o.host().Stderr)
}
