// Package runlog keeps the logs: events.log has one line per event of
// every machine, logs/<server>.log has the full output of each run. Neither holds tokens: the
// code never prints them.
package runlog

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Log is one run of one action on one machine.
type Log struct {
	server, action, eventsLog string
	transcript                *os.File
	Out, Err                  io.Writer // the terminal and the transcript
	final                     bool
	mu                        sync.Mutex
	now                       func() time.Time
}

// Open starts a run: it writes the header into logs/<server>.log and returns the log. Output
// written to Out and Err goes to the terminal and into the transcript.
func Open(stateDir, server, action string, stdout, stderr io.Writer) (*Log, error) {
	logDir := filepath.Join(stateDir, "logs")
	for _, d := range []string{stateDir, logDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
		if err := os.Chmod(d, 0o700); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(filepath.Join(logDir, server+".log"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	l := &Log{server: server, action: action, eventsLog: filepath.Join(stateDir, "events.log"), transcript: f, now: time.Now}
	fmt.Fprintf(f, "\n===== %s %s %s\n", l.stamp(), action, server)
	l.Out = io.MultiWriter(stdout, f)
	l.Err = io.MultiWriter(stderr, f)
	return l, nil
}

// the local time with its offset to UTC, so entries stay unambiguous (2026-10-21T15:12:03+02:00)
func (l *Log) stamp() string { return l.now().Format("2006-01-02T15:04:05-07:00") }

// Event records one line in events.log.
func (l *Log) Event(format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.eventsLog, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %-24s %-9s %s\n", l.stamp(), l.server, l.action, fmt.Sprintf(format, a...))
}

// Final records the outcome of the run. Without one, a failing run is logged as FAILED.
func (l *Log) Final(format string, a ...any) {
	l.Event(format, a...)
	l.final = true
}

// Close ends the run; err is its result (nil = success).
func (l *Log) Close(err error, exitCode int) {
	if err != nil && !l.final {
		l.Event("FAILED (exit %d; see logs/%s.log)", exitCode, l.server)
	}
	l.transcript.Close()
}

// Interrupted records a run ended by Ctrl-C, a closed terminal or kill.
func (l *Log) Interrupted() {
	l.Final("interrupted")
}
