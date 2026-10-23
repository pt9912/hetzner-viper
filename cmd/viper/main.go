// viper runs the hetzner-viper scripts in their container, like the Makefile does, so that
// Docker is all a user needs: viper up web --repo https://example.org/org/web.git
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// set at build time: -ldflags "-X main.version=v0.4.0"
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	name, o, err := parse(args)
	switch {
	case errors.Is(err, errHelp):
		fmt.Print(helpText())
		return 0
	case err != nil:
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if name == "version" {
		fmt.Println("viper", version)
		return 0
	}
	err = dispatch(commands[name], &o)
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	var es exitStatus
	if !errors.As(err, &ee) && !errors.As(err, &es) {
		fmt.Fprintln(os.Stderr, "viper:", err)
	}
	return exitCode(err)
}

// replaced by the tests
var inContainer = execute

// A native command runs on this computer, unless --container asks for the container.
func dispatch(c command, o *options) error {
	if c.native != nil && !o.container {
		return c.native(o)
	}
	return inContainer(c, *o)
}

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
