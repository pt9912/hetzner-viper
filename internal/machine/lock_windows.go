package machine

// Windows is not supported; this only keeps the package building there.
func lockFile(string) (func(), error) { return func() {}, nil }
