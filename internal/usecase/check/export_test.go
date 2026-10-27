package check

// ParseProbe exports parseProbe for the tests.
func ParseProbe(out string) map[string]string { return parseProbe(out) }

// Probe exports the probe script for the tests.
func Probe() string { return probe }
