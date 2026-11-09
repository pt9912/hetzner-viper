package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

// groupServerWait is how long a client of a new group waits for its server: the server has to be
// reached by ssh and set up first, and the clients are started while that happens.
const groupServerWait = 15 * time.Minute

// gate opens when a machine of the group has its server at the provider (ok) or its run ended
// without that (not ok). Machines that depend on it wait for it.
type gate struct {
	ch   chan struct{}
	once sync.Once
	ok   bool
}

func newGate() *gate { return &gate{ch: make(chan struct{})} }

func (g *gate) open(ok bool) {
	g.once.Do(func() {
		g.ok = ok
		close(g.ch)
	})
}

// wait returns once the gate is open or ctx ends: whether it opened with success, and whether it is open
// at all. A gate that is open says more than a run that was stopped.
func (g *gate) wait(ctx context.Context) (ok, open bool) {
	select {
	case <-g.ch:
		return g.ok, true
	case <-ctx.Done():
	}
	select {
	case <-g.ch:
		return g.ok, true
	default:
		return false, false
	}
}

// groupPlan is who has to be there before whom (docs/plan/gruppen.md): the first machine with NATS
// becomes the server of a new group, so every other machine of the NATS group waits until its
// server exists. The first machine that uses a login key and firewall waits for nobody, but the
// others that use the same ones wait until it has made them, so that no two runs make them at once.
type groupPlan struct {
	order      []string // the machines: the server first, then the first of each key, then the rest
	natsLeader string   // "" without NATS
	deps       map[string][]string
}

func planGroupUp(machines []string, cfgs map[string]*model.Machine) groupPlan {
	p := groupPlan{deps: map[string][]string{}}
	leaders := map[string]string{} // key and firewall -> first machine that uses them
	class := func(m string) string { return cfgs[m].KeyName + "|" + cfgs[m].FirewallName }
	for _, m := range machines {
		if p.natsLeader == "" && cfgs[m].NATS.Enabled {
			p.natsLeader = m
		}
		if _, ok := leaders[class(m)]; !ok {
			leaders[class(m)] = m
		}
	}
	for _, m := range machines {
		var deps []string
		if p.natsLeader != "" && cfgs[m].NATS.Enabled && m != p.natsLeader {
			deps = append(deps, p.natsLeader)
		}
		if l := leaders[class(m)]; l != m && !slices.Contains(deps, l) {
			deps = append(deps, l)
		}
		p.deps[m] = deps
	}
	p.order = p.orderOf(machines)
	return p
}

// orderOf puts the machines that wait for nobody first (the server of the group before the others),
// so that they take the places of a limited run first.
func (p groupPlan) orderOf(machines []string) []string {
	var order []string
	if p.natsLeader != "" {
		order = append(order, p.natsLeader)
	}
	for _, wantFree := range []bool{true, false} {
		for _, m := range machines {
			if m != p.natsLeader && (len(p.deps[m]) == 0) == wantFree {
				order = append(order, m)
			}
		}
	}
	return order
}

// runGroupUp starts the machines of a group at the same time. The checks of all machines run first
// and nothing is made if one fails; then the machines start, each after the ones it waits for.
func runGroupUp(ctx context.Context, o *options, env settings.Env, stdout, stderr io.Writer) error {
	g, err := groupMachines(o, env)
	if err != nil {
		return err
	}
	ctx, stop := notifyContext(ctx)
	defer stop()
	cfgs := map[string]*model.Machine{}
	for _, m := range g.Machines {
		c, err := o.host().Services.Settings.Load(settingsDir(o), m, env, true)
		if err != nil {
			return fmt.Errorf("%s: %w", m, err)
		}
		cfgs[m] = c
	}
	ws := newGroupWriters(g.Machines, stdout, stderr)

	// 1. the checks of all machines, before anything is made
	var pre []driving.GroupTask
	for _, m := range g.Machines {
		mo := *o
		mo.machine, mo.group = m, ""
		pre = append(pre, driving.GroupTask{Machine: m, Run: ws.task(m, func(ctx context.Context, out, errOut io.Writer) error {
			return runPreflight(ctx, &mo, env, out, errOut)
		})})
	}
	if res := o.host().Services.Groups.Run(ctx, pre, o.parallel); driving.GroupExitCode(res) != 0 {
		fmt.Fprintln(stderr, "Nothing was made: the checks of the group failed.")
		return finishGroup(res, stdout, ws)
	}

	// 2. the machines
	plan := planGroupUp(g.Machines, cfgs)
	gates := map[string]*gate{}
	for _, m := range g.Machines {
		gates[m] = newGate()
	}
	st := &groupUpState{plan: plan, gates: gates}
	tasks := make([]driving.GroupTask, 0, len(plan.order))
	for _, m := range plan.order {
		tasks = append(tasks, driving.GroupTask{Machine: m, Run: groupUpTask(m, o, env, st, ws)})
	}
	res := o.host().Services.Groups.Run(ctx, tasks, o.parallel)
	byName := map[string]driving.GroupResult{}
	for _, r := range res {
		byName[r.Machine] = r
	}
	ordered := make([]driving.GroupResult, 0, len(res))
	for _, m := range g.Machines {
		ordered = append(ordered, byName[m])
	}
	return finishGroup(ordered, stdout, ws)
}

// groupUpState is what the tasks of a group start share.
type groupUpState struct {
	plan  groupPlan
	gates map[string]*gate

	mu           sync.Mutex
	cancels      []context.CancelFunc // of the clients that are running
	leaderFailed bool
}

// addClient makes the run of a client stoppable when the server of the group fails; it returns the
// context of the run and what to call when it is over.
func (st *groupUpState) addClient(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.leaderFailed {
		cancel()
	} else {
		st.cancels = append(st.cancels, cancel)
	}
	return ctx, cancel
}

// serverFailed stops the clients that are running.
func (st *groupUpState) serverFailed() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.leaderFailed = true
	for _, c := range st.cancels {
		c()
	}
}

func (st *groupUpState) failed() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.leaderFailed
}

// waitFor waits for the machines m depends on; an error says why m does not start.
func (st *groupUpState) waitFor(ctx context.Context, m string, noJitter bool) error {
	for _, dep := range st.plan.deps[m] {
		ok, open := st.gates[dep].wait(ctx)
		if !open {
			return ctx.Err()
		}
		if !ok {
			return skipped{fmt.Sprintf("skipped: %s failed before its server existed", dep)}
		}
	}
	if len(st.plan.deps[m]) == 0 || noJitter {
		return nil
	}
	// machines that start together should not ask the provider at the same moment
	select {
	case <-time.After(time.Duration(rand.Int64N(int64(3 * time.Second)))): //nolint:gosec // only spreads out the start
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// skipped is the error of a machine that did not start because one it waits for failed.
type skipped struct{ why string }

func (s skipped) Error() string { return s.why }

// groupUpTask is the start of one machine: wait for the machines it depends on, run viper up for it,
// and open its gate. If the server of the group fails, the clients that are still running are stopped.
func groupUpTask(m string, o *options, env settings.Env, st *groupUpState, ws *groupWriters) func(context.Context) (int, error) {
	isServer := m == st.plan.natsLeader
	inner := ws.task(m, func(ctx context.Context, out, errOut io.Writer) error {
		if !isServer {
			var done context.CancelFunc
			ctx, done = st.addClient(ctx)
			defer done()
		}
		if err := st.waitFor(ctx, m, o.noJitter); err != nil {
			return err
		}
		mo := *o
		mo.machine, mo.group = m, ""
		mo.noCode, mo.skipPreflight, mo.serverWait = true, true, groupServerWait
		mo.onServerCreated = func() { st.gates[m].open(true) }
		return runUp(ctx, &mo, env, out, errOut)
	})
	return func(ctx context.Context) (int, error) {
		code, err := inner(ctx)
		st.gates[m].open(false) // no effect if it opened already
		failed := code != 0 || err != nil
		if isServer && failed {
			st.serverFailed()
		}
		var sk skipped
		if !isServer && failed && st.failed() && ctx.Err() == nil && !errors.As(err, &sk) {
			return 1, fmt.Errorf("stopped because the server %s failed", st.plan.natsLeader)
		}
		return code, err
	}
}
