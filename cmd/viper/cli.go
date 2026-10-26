package main

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type options struct {
	machine, repo, cmd, dir   string
	force, full, noCode, tmux bool
	h                         *host // the outside world; nil means the real one
}

// host returns the outside world of the command, the real one unless a test set another.
func (o *options) host() *host {
	if o.h == nil {
		o.h = defaultHost()
	}
	return o.h
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

// isValueFlag reports whether the option takes a value.
func isValueFlag(n string) bool { return n == "repo" || n == "cmd" || n == "dir" }

// commands are the commands of viper by name.
func commands() map[string]command {
	return map[string]command{
		"setup":     {usage: "", machine: noMachine, run: nativeSetup},
		"preflight": {usage: "<machine> [--repo URL]", machine: needMachine, flags: []string{"repo"}, run: nativePreflight},
		"up":        {usage: "<machine> [--repo URL] [--no-code]", machine: needMachine, flags: []string{"repo", "no-code"}, run: nativeUp},
		"check":     {usage: "<machine>", machine: needMachine, run: nativeCheck},
		"ssh":       {usage: "<machine> [--cmd COMMAND | --tmux]", machine: needMachine, flags: []string{"cmd", "tmux"}, run: nativeSSH},
		"down":      {usage: "<machine> [--force]", machine: needMachine, flags: []string{"force"}, run: nativeDown},
		"vscode":    {usage: "<machine>", machine: needMachine, run: nativeVSCode},
		"dns":       {usage: "<machine>", machine: needMachine, run: nativeDNS},
		"log":       {usage: "[<machine>] [--full]", machine: maybeMachine, flags: []string{"full"}, run: nativeLog},
		"version":   {usage: "", machine: noMachine},
	}
}

func parse(args []string) (string, options, error) {
	var o options
	name, c, err := lookupCommand(args)
	if err != nil {
		return "", o, err
	}
	allowed := map[string]bool{}
	for _, f := range append([]string{"dir"}, c.flags...) {
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
		if err := parseFlag(&o, name, args, &i, allowed); err != nil {
			return "", o, err
		}
	}
	if err := setMachine(&o, name, c, positional); err != nil {
		return "", o, err
	}
	return name, o, nil
}

// lookupCommand finds the command named by the first argument.
func lookupCommand(args []string) (string, command, error) {
	if len(args) == 0 {
		return "", command{}, errHelp
	}
	name := args[0]
	if name == "help" || name == "-h" || name == "--help" {
		return "", command{}, errHelp
	}
	c, ok := commands()[name]
	if !ok {
		return "", command{}, fmt.Errorf("unknown command %q (viper help lists them)", name)
	}
	return name, c, nil
}

// setMachine takes the machine name from the positional arguments, as the command requires.
func setMachine(o *options, name string, c command, positional []string) error {
	if len(positional) > 0 {
		if c.machine == noMachine || len(positional) > 1 {
			return fmt.Errorf("viper %s: unexpected argument %q\n%s", name, positional[len(positional)-1], usageLine(name))
		}
		o.machine = positional[0]
	}
	if c.machine == needMachine && o.machine == "" {
		return fmt.Errorf("%s", usageLine(name))
	}
	return nil
}

func usageLine(name string) string {
	return strings.TrimRight("Usage: viper "+name+" "+commands()[name].usage, " ")
}

func helpText() string {
	names := make([]string, 0, len(commands()))
	for n := range commands() {
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

` + manualLines(version))
	return b.String()
}

var releaseVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

const manualBase = "https://github.com/pt9912/hetzner-viper/blob/"

// manualLines name the user manual (German): of this version, if it is a release, and the current
// one on main.
func manualLines(v string) string {
	const path = "/docs/user/handbuch.md"
	if !releaseVersion.MatchString(v) {
		return "Manual (German): " + manualBase + "main" + path + "\n"
	}
	return "Manual (German), this version: " + manualBase + v + path + "\n" +
		"                      current:      " + manualBase + "main" + path + "\n"
}

// parseFlag handles the option args[*i] (starting with "--"); a value in the next argument advances *i.
func parseFlag(o *options, name string, args []string, i *int, allowed map[string]bool) error {
	n, v, hasValue := strings.Cut(args[*i][2:], "=")
	if !allowed[n] {
		return fmt.Errorf("viper %s: unknown option --%s\n%s", name, n, usageLine(name))
	}
	if isValueFlag(n) {
		if !hasValue {
			if *i++; *i >= len(args) {
				return fmt.Errorf("viper %s: --%s needs a value", name, n)
			}
			v = args[*i]
		}
		switch n {
		case "repo":
			o.repo = v
		case "cmd":
			o.cmd = v
		case "dir":
			o.dir = v
		}
		return nil
	}
	if hasValue {
		return fmt.Errorf("viper %s: --%s takes no value", name, n)
	}
	switch n {
	case "force":
		o.force = true
	case "full":
		o.full = true
	case "no-code":
		o.noCode = true
	case "tmux":
		o.tmux = true
	}
	return nil
}
