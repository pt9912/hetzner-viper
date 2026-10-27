package system

import "io"

// Windows is not supported (see execute); this only keeps the package building there.
// Exec runs the program as a child on the given streams.
func Exec(stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	return RunAttached(stdin, stdout, stderr, name, args...)
}
