package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

// groupAsker makes one reader of the answers serve all machines of a group: only one machine asks at a
// time, and the prompt names the machine it is for. The machines run at the same time, so a machine
// that asks waits until the one before has its answer.
func groupAsker(ctx context.Context, in io.Reader, prompts io.Writer) func(machine string) func(string) string {
	read := newAsker(ctx, in, prompts)
	var mu sync.Mutex
	return func(machine string) func(string) string {
		return func(prompt string) string {
			mu.Lock()
			defer mu.Unlock()
			return read("[" + machine + "] " + prompt)
		}
	}
}

// runGroupDown deletes the machines of a group: first all that are not the NATS server, at the same
// time, then the server (it refuses while others of its group exist). If a machine of the first wave is
// not deleted, the server stays.
func runGroupDown(ctx context.Context, o *options, env settings.Env, in io.Reader, stdout, stderr io.Writer) error {
	g, err := groupMachines(o, env)
	if err != nil {
		return err
	}
	ctx, stop := notifyContext(ctx)
	defer stop()
	ws := newGroupWriters(g.Machines, stdout, stderr)
	askFor := groupAsker(ctx, in, stderr)
	roles := map[string]model.NATSRole{}
	for _, m := range g.Machines {
		c, err := o.host().Services.Settings.Load(settingsDir(o), m, env, true)
		if err != nil {
			return fmt.Errorf("%s: %w", m, err)
		}
		if r, ok := o.host().Services.Down(c, driving.DownOptions{}).(driving.GroupRoler); ok {
			if role, _, found, err := r.GroupRole(ctx, c); err == nil && found {
				roles[m] = role
			}
		}
	}
	taskOf := func(m string) driving.GroupTask {
		mo := *o
		mo.machine, mo.group, mo.ask = m, "", askFor(m)
		return driving.GroupTask{Machine: m, Run: ws.task(m, func(ctx context.Context, out, errOut io.Writer) error {
			return runDown(ctx, &mo, env, nil, out, errOut)
		})}
	}
	var wave1, wave2 []driving.GroupTask
	for _, m := range g.Machines {
		if roles[m] == model.NATSServer {
			wave2 = append(wave2, taskOf(m))
		} else {
			wave1 = append(wave1, taskOf(m))
		}
	}
	results := o.host().Services.Groups.Run(ctx, wave1, o.parallel)
	if bad := notDone(results); len(bad) > 0 {
		for _, t := range wave2 {
			results = append(results, driving.SkippedResult(t.Machine, "the server stays: "+strings.Join(bad, ", ")+" not deleted"))
		}
	} else {
		results = append(results, o.host().Services.Groups.Run(ctx, wave2, o.parallel)...)
	}
	byName := map[string]driving.GroupResult{}
	for _, r := range results {
		byName[r.Machine] = r
	}
	ordered := make([]driving.GroupResult, 0, len(results))
	for _, m := range g.Machines {
		ordered = append(ordered, byName[m])
	}
	return finishGroup(ordered, stdout, ws)
}

// notDone are the machines whose run was not fine.
func notDone(results []driving.GroupResult) []string {
	var bad []string
	for _, r := range results {
		if !r.OK() {
			bad = append(bad, r.Machine)
		}
	}
	return bad
}
