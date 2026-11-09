package driving

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

// GroupRunner runs the same command for the machines of a group at the same time (docs/plan/gruppen.md).
// It knows nothing of what the command does: the caller gives it one task for each machine.
type GroupRunner interface {
	// Run runs the tasks, at most limit at the same time (limit < 1: DefaultGroupParallel), and returns
	// the results in the order of the tasks. A task that has not started when ctx ends does not start.
	Run(ctx context.Context, tasks []GroupTask, limit int) []GroupResult
}

// DefaultGroupParallel is how many machines run at the same time unless the caller says otherwise: the
// API of the provider limits the rate, and the installers of Claude Code and Homebrew limit by address.
const DefaultGroupParallel = 8

// GroupTask is the work for one machine. It returns the exit code of the machine (0: fine); a non-nil
// error is a failure whose reason the task has shown already.
type GroupTask struct {
	Machine string
	Run     func(ctx context.Context) (int, error)
}

// GroupResult is what became of the task of one machine.
type GroupResult struct {
	Machine  string
	Code     int
	Err      error
	Skipped  string // why the task did not run ("" if it ran)
	Canceled bool
	Elapsed  time.Duration
}

// OK reports whether the machine is fine.
func (r GroupResult) OK() bool { return r.Skipped == "" && !r.Canceled && r.Err == nil && r.Code == 0 }

// SkippedResult is the result of a machine whose task was not run because of an earlier one.
func SkippedResult(machine, why string) GroupResult {
	return GroupResult{Machine: machine, Skipped: why}
}

// GroupExitCode is the exit status of a group command: 130 if it was interrupted, 1 if a machine failed
// or was skipped, else 0.
func GroupExitCode(results []GroupResult) int {
	code := 0
	for _, r := range results {
		switch {
		case r.Canceled:
			return 130
		case !r.OK():
			code = 1
		}
	}
	return code
}

// WriteGroupTable writes the table of the results, one line for each machine.
func WriteGroupTable(w io.Writer, results []GroupResult) {
	width := 0
	for _, r := range results {
		width = max(width, len(r.Machine))
	}
	fmt.Fprintln(w, "== Result")
	for _, r := range results {
		fmt.Fprintf(w, "%-*s  %s\n", width, r.Machine, describe(r))
	}
}

func describe(r GroupResult) string {
	switch {
	case r.Skipped != "":
		return "skipped: " + r.Skipped
	case r.Canceled:
		return "canceled"
	case r.Err != nil:
		return "failed: " + strings.TrimSpace(r.Err.Error())
	case r.Code != 0:
		return fmt.Sprintf("failed (exit status %d, %s)", r.Code, r.Elapsed.Round(time.Second))
	}
	return fmt.Sprintf("ok (%s)", r.Elapsed.Round(time.Second))
}
