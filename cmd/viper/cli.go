package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type options struct {
	machine, repo, cmd, dir string
	force, full             bool
}

type machineMode int

const (
	noMachine machineMode = iota
	needMachine
	maybeMachine
)

type command struct {
	usage   string // shown after "viper <name>"
	machine machineMode
	flags   []string // options besides the global ones
	run     func(o *options) error
}

var errHelp = errors.New("help")

var globalFlags = []string{"dir"}
var valueFlags = map[string]bool{"repo": true, "cmd": true, "dir": true}

var commands = map[string]command{
	"setup":     {usage: "", machine: noMachine, run: nativeSetup},
	"preflight": {usage: "<machine> [--repo URL]", machine: needMachine, flags: []string{"repo"}, run: nativePreflight},
	"up":        {usage: "<machine> [--repo URL]", machine: needMachine, flags: []string{"repo"}, run: nativeUp},
	"check":     {usage: "<machine>", machine: needMachine, run: nativeCheck},
	"ssh":       {usage: "<machine> [--cmd COMMAND]", machine: needMachine, flags: []string{"cmd"}, run: nativeSSH},
	"down":      {usage: "<machine> [--force]", machine: needMachine, flags: []string{"force"}, run: nativeDown},
	"dns":       {usage: "<machine>", machine: needMachine, run: nativeDNS},
	"log":       {usage: "[<machine>] [--full]", machine: maybeMachine, flags: []string{"full"}, run: nativeLog},
	"version":   {usage: "", machine: noMachine},
}

func parse(args []string) (string, options, error) {
	var o options
	if len(args) == 0 {
		return "", o, errHelp
	}
	name := args[0]
	if name == "help" || name == "-h" || name == "--help" {
		return "", o, errHelp
	}
	c, ok := commands[name]
	if !ok {
		return "", o, fmt.Errorf("unknown command %q (viper help lists them)", name)
	}
	allowed := map[string]bool{}
	for _, f := range append(append([]string{}, globalFlags...), c.flags...) {
		allowed[f] = true
	}
	var positional []string
	for i := 1; i < len(args); i++ {
		a := args[i]
		if a == "-h" || a == "--help" {
			return "", o, errHelp
		}
		if !strings.HasPrefix(a, "--") {
			positional = append(positional, a)
			continue
		}
		n, v, hasValue := strings.Cut(a[2:], "=")
		if !allowed[n] {
			return "", o, fmt.Errorf("viper %s: unknown option --%s\n%s", name, n, usageLine(name))
		}
		if valueFlags[n] {
			if !hasValue {
				if i++; i >= len(args) {
					return "", o, fmt.Errorf("viper %s: --%s needs a value", name, n)
				}
				v = args[i]
			}
			switch n {
			case "repo":
				o.repo = v
			case "cmd":
				o.cmd = v
			case "dir":
				o.dir = v
			}
			continue
		}
		if hasValue {
			return "", o, fmt.Errorf("viper %s: --%s takes no value", name, n)
		}
		switch n {
		case "force":
			o.force = true
		case "full":
			o.full = true
		}
	}
	if len(positional) > 0 {
		if c.machine == noMachine || len(positional) > 1 {
			return "", o, fmt.Errorf("viper %s: unexpected argument %q\n%s", name, positional[len(positional)-1], usageLine(name))
		}
		o.machine = positional[0]
	}
	if c.machine == needMachine && o.machine == "" {
		return "", o, fmt.Errorf("%s", usageLine(name))
	}
	return name, o, nil
}

func usageLine(name string) string {
	return strings.TrimRight("Usage: viper "+name+" "+commands[name].usage, " ")
}

func helpText() string {
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("viper " + version + ": set up and use Hetzner machines for Claude Code (needs the OpenSSH client)\n\n")
	for _, n := range names {
		b.WriteString("  " + strings.TrimPrefix(usageLine(n), "Usage: ") + "\n")
	}
	b.WriteString(`
Option for every command:
  --dir DIR   the directory with .env.local (and machines/); a relative SSH_KEY is relative to it
              (default: the current directory)

Values that start with a dash need the form --cmd=VALUE, for example viper ssh web --cmd="ls -la".
`)
	return b.String()
}
