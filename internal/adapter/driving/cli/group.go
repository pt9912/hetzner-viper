package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

// groupMachines reads the group of --group.
func groupMachines(o *options, env settings.Env) (*model.Group, error) {
	return o.host().Services.Settings.LoadGroup(settingsDir(o), o.group, env)
}

// machineRun is the command for one machine of a group; out and errOut get the prefix of the machine.
type machineRun func(ctx context.Context, o *options, env settings.Env, out, errOut io.Writer) error

// runGroup runs a command for every machine of the group at the same time and prints the table. Its
// error is the exit status of the whole command.
func runGroup(ctx context.Context, o *options, env settings.Env, stdout, stderr io.Writer, run machineRun) error {
	g, err := groupMachines(o, env)
	if err != nil {
		return err
	}
	ctx, stop := notifyContext(ctx)
	defer stop()
	var mu sync.Mutex
	width := 0
	for _, m := range g.Machines {
		width = max(width, len(m))
	}
	tasks := make([]driving.GroupTask, 0, len(g.Machines))
	for _, m := range g.Machines {
		po, pe := newPrefixWriter(&mu, stdout, m, width), newPrefixWriter(&mu, stderr, m, width)
		mo := *o
		mo.machine, mo.group = m, ""
		tasks = append(tasks, driving.GroupTask{Machine: m, Run: func(ctx context.Context) (int, error) {
			defer po.flush()
			defer pe.flush()
			err := run(ctx, &mo, env, po, pe)
			var es exitStatus
			switch {
			case err == nil:
				return 0, nil
			case errors.As(err, &es):
				return int(es), nil
			}
			fmt.Fprintln(pe, err)
			return 1, err
		}})
	}
	results := o.host().Services.Groups.Run(ctx, tasks, o.parallel)
	mu.Lock()
	driving.WriteGroupTable(stdout, results)
	mu.Unlock()
	if code := driving.GroupExitCode(results); code != 0 {
		return exitStatus(code)
	}
	return nil
}

// sshOnce runs one command on a machine and returns what it printed (the machines of a group cannot
// share the terminal, so no ssh takes the place of viper).
func sshOnce(ctx context.Context, o *options, env settings.Env, out, _ io.Writer) error {
	c, err := o.host().Services.Settings.Load(settingsDir(o), o.machine, env, false)
	if err != nil {
		return err
	}
	if !exists(c.SSHConfig) {
		return fmt.Errorf("No SSH config for %s: run viper up %s first", c.ServerName, o.machine)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	text, err := o.host().OutputTool("ssh", "-n", "-F", c.SSHConfig, "-o", "BatchMode=yes", c.SSHAlias, o.cmd)
	if text != "" {
		fmt.Fprint(out, strings.TrimRight(text, "\n")+"\n")
	}
	if err != nil {
		return exitStatus(1)
	}
	return nil
}
