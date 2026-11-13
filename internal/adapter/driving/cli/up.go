package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

// runUp creates or reuses the machine and sets it up.
func runUp(ctx context.Context, o *options, env settings.Env, stdout, stderr io.Writer) error {
	c, err := o.host().Services.Settings.Load(settingsDir(o), o.machine, env, true)
	if err != nil {
		return err
	}
	l, err := o.host().Services.OpenLog(c, "up", stdout, stderr)
	if err != nil {
		return err
	}
	ctx, stop := notifyContext(ctx)
	defer stop()
	up := o.host().Services.Up(c, driving.UpOptions{
		NoCode:          o.noCode,
		OnServerCreated: o.onServerCreated,
		SkipPreflight:   o.skipPreflight,
		ServerWait:      o.serverWait,
		// the pauses end with an interruption
		Sleep: func(d time.Duration) {
			select {
			case <-time.After(d):
			case <-ctx.Done():
			}
		},
	})
	code, err := up.Up(ctx, c, o.repo, l, l.Out(), l.Err())
	switch {
	case ctx.Err() != nil:
		l.Interrupted()
		l.Close(ctx.Err(), 130)
		return exitStatus(130)
	case err != nil && err != driving.ErrShown:
		l.Close(err, 1)
		return err
	case err != nil:
		l.Close(err, code)
		return exitStatus(code)
	}
	l.Close(nil, code)
	if code != 0 {
		return exitStatus(code)
	}
	return nil
}

func nativeUp(o *options) error {
	// before anything is created: a program that is missing later would leave a server behind
	for _, program := range []string{"ssh", "ssh-keygen", "scp"} {
		if err := o.host().need(program); err != nil {
			return err
		}
	}
	env, err := o.env()
	if err != nil {
		return err
	}
	if o.group != "" {
		if o.noCode {
			return fmt.Errorf("viper up --group: --no-code is the default for a group, VS Code is not opened")
		}
		return runGroupUp(context.Background(), o, env, o.host().Stdout, o.host().Stderr)
	}
	return runUp(context.Background(), o, env, o.host().Stdout, o.host().Stderr)
}
