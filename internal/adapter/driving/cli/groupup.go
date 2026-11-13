package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"path/filepath"
	"slices"
	"strings"
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

// groupPlan is who has to be there before whom (docs/plan/gruppen.md): in a new NATS group the first
// machine with NATS becomes the server, so every other machine of the NATS group waits until its
// server exists. If the group exists already (some machine has its labels), nobody waits for a server.
// The first machine that uses a login key and firewall waits for nobody, but the others that use the
// same ones wait until it has made them, so that no two runs make them at once.
type groupPlan struct {
	order      []string        // each machine after the ones it waits for
	natsLeader string          // the machine that becomes the server of a new group; "" without NATS or in an existing group
	members    map[string]bool // the machines in the NATS group: they depend on the server
	deps       map[string][]string
}

func planGroupUp(machines []string, cfgs map[string]*model.Machine, groupExists bool) groupPlan {
	p := groupPlan{deps: map[string][]string{}, members: map[string]bool{}}
	leaders := map[string]string{} // key and firewall -> first machine that uses them
	class := func(m string) string { return cfgs[m].KeyName + "|" + cfgs[m].FirewallName }
	for _, m := range machines {
		p.members[m] = cfgs[m].NATS.Enabled
		if p.natsLeader == "" && cfgs[m].NATS.Enabled && !groupExists {
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

// orderOf sorts the machines so that each comes after the ones it waits for (they take the places of a
// limited run in this order: a machine that waits must not hold the only place its dependency needs).
// Among the machines that can start, the server of a new group comes first, then the ones that wait for
// nobody, then the rest, each in the order of the file.
func (p groupPlan) orderOf(machines []string) []string {
	placed := map[string]bool{}
	var order []string
	for len(order) < len(machines) {
		pick := p.next(machines, placed)
		if pick == "" {
			return append(order, machines...) // a cycle cannot happen; do not lose a machine if it does
		}
		placed[pick] = true
		order = append(order, pick)
	}
	return order
}

// next is the machine that starts next: the server if it can, else one that waits for nobody, else any
// whose dependencies are placed; "" if there is none.
func (p groupPlan) next(machines []string, placed map[string]bool) string {
	ready := func(m string) bool {
		if placed[m] {
			return false
		}
		for _, d := range p.deps[m] {
			if !placed[d] {
				return false
			}
		}
		return true
	}
	for _, want := range []func(string) bool{
		func(m string) bool { return m == p.natsLeader },
		func(m string) bool { return len(p.deps[m]) == 0 },
		func(string) bool { return true },
	} {
		for _, m := range machines {
			if ready(m) && want(m) {
				return m
			}
		}
	}
	return ""
}

// groupExists reports whether some machine of the group has its labels already: then the group has
// its server, and nobody waits for one. A role that cannot be read counts as not existing.
func groupExists(ctx context.Context, o *options, cfgs map[string]*model.Machine, machines []string) bool {
	if o.host().Services.Down == nil {
		return false
	}
	for _, m := range machines {
		if !cfgs[m].NATS.Enabled {
			continue
		}
		if r, ok := o.host().Services.Down(cfgs[m], driving.DownOptions{}).(driving.GroupRoler); ok {
			if _, _, found, err := r.GroupRole(ctx, cfgs[m]); err == nil && found {
				return true
			}
		}
	}
	return false
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
	noticeMachinesWithoutFile(o, g.Machines, stdout)

	// 1. the checks of all machines, before anything is made
	var pre []driving.GroupTask
	for _, m := range g.Machines {
		mo := *o
		mo.within = o.group
		mo.machine, mo.group = m, ""
		pre = append(pre, driving.GroupTask{Machine: m, Run: ws.task(m, func(ctx context.Context, out, errOut io.Writer) error {
			return runPreflight(ctx, &mo, env, out, errOut)
		})})
	}
	if res := o.host().Services.Groups.Run(ctx, pre, o.parallel); driving.GroupExitCode(res) != 0 {
		if ctx.Err() == nil {
			fmt.Fprintln(stderr, "Nothing was made: the checks of the group failed.")
		}
		return finishGroup(res, stdout, ws)
	}

	// 2. the machines
	plan := planGroupUp(g.Machines, cfgs, groupExists(ctx, o, cfgs, g.Machines))
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
	cancels      map[string]context.CancelFunc // of the clients that are running
	stopped      map[string]bool               // the clients that were stopped because the server failed
	leaderFailed bool
}

// addClient makes the run of a client of the new group stoppable when its server fails; it returns the
// context of the run and what to call when it is over.
func (st *groupUpState) addClient(ctx context.Context, m string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.cancels == nil {
		st.cancels, st.stopped = map[string]context.CancelFunc{}, map[string]bool{}
	}
	if st.leaderFailed {
		st.stopped[m] = true
		cancel()
	} else {
		st.cancels[m] = cancel
	}
	return ctx, cancel
}

// serverFailed stops the clients that are running.
func (st *groupUpState) serverFailed() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.leaderFailed = true
	for m, c := range st.cancels {
		st.stopped[m] = true
		c()
	}
}

// wasStopped reports that the run of m was stopped because the server failed (and did not fail by itself).
func (st *groupUpState) wasStopped(m string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.stopped[m]
}

// waitFor waits for the machines m depends on; an error says why m does not start.
func (st *groupUpState) waitFor(ctx context.Context, m string, noJitter bool) error {
	for _, dep := range st.plan.deps[m] {
		ok, open := st.gates[dep].wait(ctx)
		if !open {
			return ctx.Err()
		}
		if !ok && dep == st.plan.natsLeader {
			return skipped{fmt.Sprintf("skipped: %s failed before its server existed", dep)}
		}
		// the first machine of a key and firewall failed: the others make them themselves
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
	isServer := m == st.plan.natsLeader && m != ""
	inner := ws.task(m, func(ctx context.Context, out, errOut io.Writer) error {
		if st.plan.natsLeader != "" && !isServer && st.plan.members[m] {
			var done context.CancelFunc
			ctx, done = st.addClient(ctx, m)
			defer done()
		}
		if err := st.waitFor(ctx, m, o.noJitter); err != nil {
			return err
		}
		mo := *o
		mo.within = o.group
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
		if failed && st.wasStopped(m) && ctx.Err() == nil && !errors.As(err, &sk) {
			return 1, fmt.Errorf("stopped because the server %s failed", st.plan.natsLeader)
		}
		return code, err
	}
}

// noticeMachinesWithoutFile says which machines of the group have no machines/<m>.env.local: they get the
// settings of the group, which is fine, unless a name in GROUP_MACHINES has a typo and makes a new server.
func noticeMachinesWithoutFile(o *options, machines []string, stdout io.Writer) {
	var without []string
	for _, m := range machines {
		if !exists(filepath.Join(settingsDir(o), "machines", m+".env.local")) {
			without = append(without, m)
		}
	}
	if len(without) > 0 {
		fmt.Fprintf(stdout, "No machines/<machine>.env.local for %s: they use the settings of the group. A name in GROUP_MACHINES with a typo would make a new server.\n", strings.Join(without, ", "))
	}
}
