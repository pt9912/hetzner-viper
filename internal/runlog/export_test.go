package runlog

import "time"

// SetNow replaces the clock of the log for the tests.
func (l *Log) SetNow(now func() time.Time) { l.now = now }
