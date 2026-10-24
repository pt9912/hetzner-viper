// viper sets up and uses Hetzner machines for Claude Code: viper up web --repo https://example.org/org/web.git
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// set at build time: -ldflags "-X main.version=v0.4.0"
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	name, o, err := parse(args)
	switch {
	case errors.Is(err, errHelp):
		fmt.Fprint(stdout, helpText())
		return 0
	case err != nil:
		fmt.Fprintln(stderr, err)
		return 2
	}
	if name == "version" {
		fmt.Fprintln(stdout, "viper", version)
		return 0
	}
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
