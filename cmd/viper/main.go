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
	if err := execute(commands[name], o); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if code := ee.ExitCode(); code >= 0 {
				return code
			}
			return 130 // docker was ended by a signal
		}
		fmt.Fprintln(os.Stderr, "viper:", err)
		return 1
	}
	return 0
}
