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
	ws := newGroupWriters(g.Machines, stdout, stderr)
	tasks := make([]driving.GroupTask, 0, len(g.Machines))
	for _, m := range g.Machines {
		mo := *o
		mo.machine, mo.group = m, ""
		tasks = append(tasks, driving.GroupTask{Machine: m, Run: ws.task(m, func(ctx context.Context, out, errOut io.Writer) error {
			return run(ctx, &mo, env, out, errOut)
		})})
	}
	return finishGroup(o.host().Services.Groups.Run(ctx, tasks, o.parallel), stdout, ws)
}

// groupWriters are the writers of the machines of a group: one lock for the terminal.
type groupWriters struct {
	mu       sync.Mutex
	out, err map[string]*prefixWriter
	// turn is held by the machine that has the terminal for a block of its output and its question
	// (only in the buffered mode of down, where a machine's lines are kept until it asks or is done).
	turn sync.Mutex
	bufs map[string]*machineBuf // nil unless buffered
}

// newBufferedGroupWriters is newGroupWriters, but what a machine says is kept until the machine asks
// a question or is done (see release): then the whole block is shown at once, so a question stands
// right below the lines it is about and the lines of other machines do not cut in.
func newBufferedGroupWriters(machines []string, stdout, stderr io.Writer) *groupWriters {
	ws := &groupWriters{out: map[string]*prefixWriter{}, err: map[string]*prefixWriter{}, bufs: map[string]*machineBuf{}}
	width := 0
	for _, m := range machines {
		width = max(width, len(m))
	}
	for _, m := range machines {
		b := &machineBuf{}
		ws.bufs[m] = b
		var own sync.Mutex // the lines of one machine come from two writers; the terminal lock is taken in release
		ws.out[m] = newPrefixWriter(&own, b.stream(stdout), m, width)
		ws.err[m] = newPrefixWriter(&own, b.stream(stderr), m, width)
	}
	return ws
}

// release shows what the machine said so far. The caller holds turn.
func (ws *groupWriters) release(m string) {
	if b := ws.bufs[m]; b != nil {
		ws.mu.Lock()
		b.writeTo()
		ws.mu.Unlock()
	}
}

// finish shows the rest of a machine's output when its task ends.
func (ws *groupWriters) finish(m string) {
	if ws.bufs == nil {
		return
	}
	ws.turn.Lock()
	defer ws.turn.Unlock()
	ws.release(m)
}

func newGroupWriters(machines []string, stdout, stderr io.Writer) *groupWriters {
	ws := &groupWriters{out: map[string]*prefixWriter{}, err: map[string]*prefixWriter{}}
	width := 0
	for _, m := range machines {
		width = max(width, len(m))
	}
	for _, m := range machines {
		ws.out[m], ws.err[m] = newPrefixWriter(&ws.mu, stdout, m, width), newPrefixWriter(&ws.mu, stderr, m, width)
	}
	return ws
}

// task makes the work of a machine a task: its output gets the prefix, and its error becomes an exit code.
func (ws *groupWriters) task(m string, run func(ctx context.Context, out, errOut io.Writer) error) func(context.Context) (int, error) {
	return func(ctx context.Context) (int, error) {
		po, pe := ws.out[m], ws.err[m]
		defer ws.finish(m)
		defer po.flush()
		defer pe.flush()
		err := run(ctx, po, pe)
		var es exitStatus
		switch {
		case err == nil:
			return 0, nil
		case errors.As(err, &es):
			return int(es), nil
		}
		fmt.Fprintln(pe, err)
		return 1, err
	}
}

// finishGroup prints the table and returns the exit status.
func finishGroup(results []driving.GroupResult, stdout io.Writer, ws *groupWriters) error {
	code := driving.GroupExitCode(results)
	ws.mu.Lock()
	driving.WriteGroupTable(stdout, results)
	ws.mu.Unlock()
	if code != 0 {
		return exitStatus(code)
	}
	return nil
}

// sshOnce runs one command on a machine and returns what it printed (the machines of a group cannot
// share the terminal, so no ssh takes the place of viper).
func sshOnce(ctx context.Context, o *options, env settings.Env, out, errOut io.Writer) error {
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
	args := []string{"-n", "-F", c.SSHConfig, "-o", "BatchMode=yes", c.SSHAlias, o.cmd}
	if st := o.host().StreamTool; st != nil {
		if err := st(ctx, "", out, errOut, "ssh", args...); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return exitStatus(1)
		}
		return nil
	}
	text, err := o.host().OutputTool("ssh", args...)
	if text != "" {
		fmt.Fprint(out, strings.TrimRight(text, "\n")+"\n")
	}
	if err != nil {
		return exitStatus(1)
	}
	return nil
}
