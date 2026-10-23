package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type options struct {
	machine, repo, cmd    string
	force, full, local    bool
	image, baseImage, dir string
	extraEnv              []string
}

type machineMode int

const (
	noMachine machineMode = iota
	needMachine
	maybeMachine
)

// A command runs the given arguments of bash in the container (a script of the checkout, or -c).
type command struct {
	usage   string // shown after "viper <name>"
	machine machineMode
	flags   []string // options besides the global ones
	args    func(o *options) []string
}

var errHelp = errors.New("help")

var globalFlags = []string{"image", "base-image", "dir", "local"}
var valueFlags = map[string]bool{"repo": true, "cmd": true, "image": true, "base-image": true, "dir": true}

const dnsScript = `set -a; for f in ./.env.local "./machines/$1.env.local"; do [ ! -f "$f" ] || . "$f"; done; ` +
	`[ -n "$DNS_DOMAIN" ] || { echo "DNS_DOMAIN is not set in .env.local" >&2; exit 1; }; ` +
	`printf "%s-%s.%s\n" "${SERVER_PREFIX:-claude}" "$1" "$DNS_DOMAIN"`

// the prefix is read as the scripts read it (bash sources the files: comments, quotes)
const logScript = `d="$HOME/.local/state/hetzner-claude"; ` +
	`if [ -z "$MACHINE" ]; then tail -n 50 "$d/events.log" 2>/dev/null || echo 'no events yet'; exit 0; fi; ` +
	`p=$(bash -c 'set -a; for f in ./.env.local "./machines/$1.env.local"; do [ ! -f "$f" ] || . "$f"; done; printf %s "$SERVER_PREFIX"' _ "$MACHINE" 2>/dev/null); ` +
	`s="${p:-claude}-$MACHINE"; ` +
	`if [ "$FULL" = 1 ]; then cat "$d/logs/$s.log" 2>/dev/null || echo "no log for $s"; ` +
	`else awk -v s="$s" '$2 == s' "$d/events.log" 2>/dev/null | grep . || echo "no events for $s"; fi`

var commands = map[string]command{
	"setup": {usage: "", machine: noMachine,
		args: func(o *options) []string { return []string{"./setup.sh"} }},
	"preflight": {usage: "<machine> [--repo URL]", machine: needMachine, flags: []string{"repo"},
		args: func(o *options) []string { return []string{"./preflight.sh", o.machine, o.repo} }},
	"up": {usage: "<machine> [--repo URL]", machine: needMachine, flags: []string{"repo"},
		args: func(o *options) []string { return []string{"./start.sh", o.machine, o.repo} }},
	"check": {usage: "<machine>", machine: needMachine,
		args: func(o *options) []string { return []string{"./check.sh", o.machine} }},
	"ssh": {usage: "<machine> [--cmd COMMAND]", machine: needMachine, flags: []string{"cmd"},
		args: func(o *options) []string {
			a := []string{"./connect.sh", o.machine}
			if o.cmd != "" {
				a = append(a, o.cmd)
			}
			return a
		}},
	"down": {usage: "<machine> [--force]", machine: needMachine, flags: []string{"force"},
		args: func(o *options) []string {
			a := []string{"./stop.sh", o.machine}
			if o.force {
				a = append(a, "--force")
			}
			return a
		}},
	"dns": {usage: "<machine>", machine: needMachine,
		args: func(o *options) []string { return []string{"-c", dnsScript, "_", o.machine} }},
	"log": {usage: "[<machine>] [--full]", machine: maybeMachine, flags: []string{"full"},
		args: func(o *options) []string {
			o.extraEnv = []string{"MACHINE=" + o.machine}
			if o.full {
				o.extraEnv = append(o.extraEnv, "FULL=1")
			}
			return []string{"-c", logScript, "_"}
		}},
	"version": {usage: "", machine: noMachine},
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
			case "image":
				o.image = v
			case "base-image":
				o.baseImage = v
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
		case "local":
			o.local = true
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
	b.WriteString("viper " + version + ": run the hetzner-viper scripts in their container (needs Docker, run it in the checkout)\n\n")
	for _, n := range names {
		b.WriteString("  " + strings.TrimPrefix(usageLine(n), "Usage: ") + "\n")
	}
	b.WriteString(`
Options for every command:
  --dir DIR         the checkout with the scripts and .env.local (default: current directory)
  --image REF       run this image (pulled if missing) instead of the one of this version
  --local           build the image from the Dockerfile instead of pulling the release's
  --base-image REF  base image for --local (default: Docker Hub, or mirror.gcr.io if unreachable)

Values that start with a dash need the form --cmd=VALUE, for example viper ssh web --cmd="ls -la".
`)
	return b.String()
}
