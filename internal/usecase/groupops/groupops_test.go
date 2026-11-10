package groupops_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/port/driving"
	"github.com/pt9912/hetzner-viper/internal/usecase/groupops"
)

func task(name string, code int, err error) driving.GroupTask {
	return driving.GroupTask{Machine: name, Run: func(context.Context) (int, error) { return code, err }}
}

func TestRunKeepsTheOrderAndSumsUp(t *testing.T) {
	res := groupops.Runner{}.Run(context.Background(), []driving.GroupTask{task("a", 0, nil), task("b", 1, nil), task("c", 0, errors.New("boom"))}, 3)
	if len(res) != 3 || res[0].Machine != "a" || !res[0].OK() || res[1].OK() || res[2].OK() {
		t.Fatalf("%+v", res)
	}
	if driving.GroupExitCode(res) != 1 {
		t.Error("a failed machine makes exit status 1")
	}
	var b bytes.Buffer
	driving.WriteGroupTable(&b, res)
	for _, want := range []string{"== Result", "a  ok", "b  failed (exit status 1", "c  failed: boom"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q in\n%s", want, b.String())
		}
	}
	if driving.GroupExitCode(res[:1]) != 0 {
		t.Error("all fine is 0")
	}
}

// waitFor waits until the condition holds (or two seconds have passed): no sleeps that must be long enough.
func waitFor(cond func() bool) {
	deadline := time.Now().Add(2 * time.Second)
	for !cond() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
}

func TestRunRunsAtTheSameTimeButNotMoreThanTheLimit(t *testing.T) {
	var running, peak atomic.Int32
	mk := func(n string, want int32) driving.GroupTask {
		return driving.GroupTask{Machine: n, Run: func(context.Context) (int, error) {
			now := running.Add(1)
			for {
				p := peak.Load()
				if now <= p || peak.CompareAndSwap(p, now) {
					break
				}
			}
			waitFor(func() bool { return peak.Load() >= want }) // all that may run are running
			running.Add(-1)
			return 0, nil
		}}
	}
	names := []string{"a", "b", "c", "d", "e", "f"}
	build := func(want int32) []driving.GroupTask {
		var ts []driving.GroupTask
		for _, n := range names {
			ts = append(ts, mk(n, want))
		}
		return ts
	}
	groupops.Runner{}.Run(context.Background(), build(3), 3)
	if peak.Load() != 3 {
		t.Errorf("peak %d, want 3", peak.Load())
	}
	peak.Store(0)
	groupops.Runner{}.Run(context.Background(), build(6), 0) // the default of 8: all six at once
	if peak.Load() != 6 {
		t.Errorf("peak %d, want 6", peak.Load())
	}
}

func TestACanceledRunStartsNothingMoreAndSaysSo(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	tasks := []driving.GroupTask{
		{Machine: "a", Run: func(ctx context.Context) (int, error) {
			close(started)
			<-ctx.Done()
			return 1, ctx.Err()
		}},
		task("b", 0, nil),
	}
	go func() { <-started; cancel() }()
	res := groupops.Runner{}.Run(ctx, tasks, 1)
	if !res[0].Canceled || !res[1].Canceled || driving.GroupExitCode(res) != 130 {
		t.Fatalf("%+v", res)
	}
	var b bytes.Buffer
	driving.WriteGroupTable(&b, res)
	if strings.Count(b.String(), "canceled") != 2 {
		t.Error(b.String())
	}
}

func TestASkippedMachineIsAProblem(t *testing.T) {
	res := []driving.GroupResult{{Machine: "a"}, driving.SkippedResult("b", "the server failed")}
	if driving.GroupExitCode(res) != 1 {
		t.Error("skipped is not fine")
	}
	var b bytes.Buffer
	driving.WriteGroupTable(&b, res)
	if !strings.Contains(b.String(), "b  skipped: the server failed") {
		t.Error(b.String())
	}
}
