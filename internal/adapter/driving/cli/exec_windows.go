package cli

// Windows is not supported (see execute); this only keeps the package building there.
func replaceProcess(name string, args ...string) error { return runChild(name, args...) }
