// Package runlog is the driven port to the log of the runs: one line per event of every machine,
// and the full output of each run. An adapter (internal/adapter/driven/runlog) keeps it in files.
package runlog

import (
	"io"

	"github.com/pt9912/hetzner-viper/internal/model"
)

// Log is what a use case records of its run.
type Log interface {
	// Event records one line.
	Event(format string, a ...any)
	// Final records the outcome of the run. Without one, a failing run is logged as FAILED.
	Final(format string, a ...any)
	// Interrupted records a run ended by Ctrl-C, a closed terminal or kill.
	Interrupted()
}

// Run is one run of one action on one machine, as the caller that started it sees it.
type Run interface {
	Log
	// Out and Err are the terminal; what is written to them is kept in the log as well.
	Out() io.Writer
	Err() io.Writer
	// Close ends the run; err is its result (nil = success).
	Close(err error, exitCode int)
}

// Opener starts a run of an action on a machine; stdout and stderr are the terminal.
type Opener func(m *model.Machine, action string, stdout, stderr io.Writer) (Run, error)

// Viewer shows the log of a server: the last run, or all of it if full.
type Viewer func(stateDir, server string, full bool, out io.Writer) error
