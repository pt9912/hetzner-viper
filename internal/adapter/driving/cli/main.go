// Package cli is the command line of viper: the driving adapter that parses the commands and calls
// the use cases through the driving ports (internal/port/driving).
package cli

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
)

// Main runs the command in args and returns the exit code. version is the release the program was
// built from; services are the use cases the commands call.
func Main(version string, args []string, stdout, stderr io.Writer, services Services) int {
	name, o, err := parse(args)
	switch {
	case errors.Is(err, errHelp):
		fmt.Fprint(stdout, helpText(version))
		return 0
	case err != nil:
		fmt.Fprintln(stderr, err)
		return 2
	}
	if name == "version" {
		fmt.Fprintln(stdout, "viper", version)
		return 0
	}
	o.h = defaultHost()
	o.h.services = services
	err = dispatch(commands()[name], &o)
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	var es exitStatus
	if !errors.As(err, &ee) && !errors.As(err, &es) {
		fmt.Fprintln(stderr, "viper:", err)
	}
	return exitCode(err)
}

func dispatch(c command, o *options) error { return c.run(o) }

func exitCode(err error) int {
	var es exitStatus
	if errors.As(err, &es) {
		return int(es)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if code := ee.ExitCode(); code >= 0 {
			return code
		}
		return 130 // the program was ended by a signal
	}
	return 1
}
