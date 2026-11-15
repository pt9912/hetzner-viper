package settingsfile

import "time"

// HelpNames are the settings that have a help text.
func HelpNames() []string {
	var out []string
	for n := range helpTexts() {
		out = append(out, n)
	}
	return out
}

// LockSettings takes the lock of the settings and waits at most the time given.
func LockSettings(path string, wait time.Duration) (func(), error) { return lockSettings(path, wait) }
