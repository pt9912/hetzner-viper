package cli

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type options struct {
	machine, repo, cmd, dir string
	version                 string // the release of this program
	tag, k3d                string // viper image build
	group                   string // run for all machines of groups/<group>.env
	// viper nats
	args                    []string // the words after the command: the subcommand, the group, the text
	to, topic, from         string
	timeout                 time.Duration
	query, asJSON, yes, all bool
	// set by the group commands for the run of one machine, not by the command line
	onServerCreated           func()
	skipPreflight             bool
	serverWait                time.Duration
	noJitter                  bool                // the tests: machines of a group start without a random pause
	ask                       func(string) string // the answers of a group: one reader, one prompt at a time
	parallel                  int                 // how many machines of a group at the same time (0: the default)
	force, full, noCode, tmux bool
	// the need of viper find
	cpu, disk                int
	ram                      float64
	arch, location, provider string
	vat                      float64
	hasVAT                   bool
	h                        *Host // the outside world
}

// host returns the outside world of the command.
func (o *options) host() *Host { return o.h }

type machineMode int

const (
	noMachine machineMode = iota
	needMachine
	maybeMachine
	restArgs // no machine: the words after the command are kept in options.args (viper nats)
)

type command struct {
	usage   string // shown after "viper <name>"
	machine machineMode
	flags   []string // options besides the global ones
	run     func(o *options) error
}

var errHelp = errors.New("help")

// isValueFlag reports whether the option takes a value.
func isValueFlag(n string) bool {
	switch n {
	case "repo", "cmd", "dir", "group", "parallel", "cpu", "ram", "disk", "arch", "location", "provider", "vat", "to", "topic", "from", "timeout", "tag", "k3d":
		return true
	}
	return false
}

// commands are the commands of viper by name.
func commands() map[string]command {
	return map[string]command{
		"setup":      {usage: "", machine: noMachine, run: nativeSetup},
		"preflight":  {usage: "<machine> [--repo URL] | --group G [--repo URL] [--parallel N]", machine: needMachine, flags: []string{"repo", "group", "parallel"}, run: nativePreflight},
		"up":         {usage: "<machine> [--repo URL] [--no-code] | --group G [--repo URL] [--parallel N]", machine: needMachine, flags: []string{"repo", "no-code", "group", "parallel"}, run: nativeUp},
		"check":      {usage: "<machine> | --group G [--parallel N]", machine: needMachine, flags: []string{"group", "parallel"}, run: nativeCheck},
		"ssh":        {usage: "<machine> [--cmd COMMAND | --tmux] | --group G --cmd COMMAND [--parallel N]", machine: needMachine, flags: []string{"cmd", "tmux", "group", "parallel"}, run: nativeSSH},
		"down":       {usage: "<machine> [--force] | --group G [--force] [--parallel N]", machine: needMachine, flags: []string{"force", "group", "parallel"}, run: nativeDown},
		"vscode":     {usage: "<machine>", machine: needMachine, run: nativeVSCode},
		"dns":        {usage: "<machine>", machine: needMachine, run: nativeDNS},
		"find":       {usage: "[--cpu N] [--ram GB] [--disk GB] [--arch x86|arm] [--location L] [--provider P] [--vat PERCENT]", machine: noMachine, flags: []string{"cpu", "ram", "disk", "arch", "location", "provider", "vat"}, run: nativeFind},
		"log":        {usage: "[<machine>] [--full]", machine: maybeMachine, flags: []string{"full"}, run: nativeLog},
		"nats":       {usage: "peers|listen|send|broadcast|request|init|revoke GROUP [TEXT|-] [--from MACHINE] [--to MACHINE] [--topic T[,T...]] [--timeout S] [--query] [--all] [--json] [--yes]", machine: restArgs, flags: []string{"to", "topic", "from", "timeout", "query", "all", "json", "yes"}, run: nativeNats},
		"image":      {usage: "build [--tag NAME:TAG] [--k3d CLUSTER]", machine: restArgs, flags: []string{"tag", "k3d"}, run: nativeImage},
		"k8s-proxy":  {usage: "<machine>", machine: needMachine, run: nativeK8sProxy},
		"mcp-binary": {usage: "--arch amd64|arm64", machine: noMachine, flags: []string{"arch"}, run: nativeMCPBinary},
		"version":    {usage: "", machine: noMachine},
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
	if c.machine == restArgs {
		o.args = positional
		return nil
	}
	if len(positional) > 0 {
		if c.machine == noMachine || len(positional) > 1 {
			return fmt.Errorf("viper %s: unexpected argument %q\n%s", name, positional[len(positional)-1], usageLine(name))
		}
		o.machine = positional[0]
	}
	if o.group != "" && o.machine != "" {
		return fmt.Errorf("viper %s: a machine and --group exclude each other\n%s", name, usageLine(name))
	}
	if c.machine == needMachine && o.machine == "" && o.group == "" {
		return fmt.Errorf("%s", usageLine(name))
	}
	return nil
}

func usageLine(name string) string {
	return strings.TrimRight("Usage: viper "+name+" "+commands()[name].usage, " ")
}

func helpText(version string) string {
	names := make([]string, 0, len(commands()))
	for n := range commands() {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("viper " + version + ": set up and use machines in the cloud for Claude Code (needs the OpenSSH client)\n\n")
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
		return setValue(o, name, n, v)
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
	case "query":
		o.query = true
	case "all":
		o.all = true
	case "json":
		o.asJSON = true
	case "yes":
		o.yes = true
	}
	return nil
}

// setNumber reads the value of --cpu, --ram or --disk: a positive number.
func setNumber(o *options, name, flag, v string) error {
	x, err := strconv.ParseFloat(v, 64)
	if err != nil || x <= 0 || math.IsNaN(x) || math.IsInf(x, 0) {
		return fmt.Errorf("viper %s: --%s needs a positive number: %s", name, flag, v)
	}
	switch flag {
	case "ram":
		o.ram = x
		return nil
	case "cpu":
		o.cpu = int(x)
	case "disk":
		o.disk = int(x)
	}
	if float64(int(x)) != x {
		return fmt.Errorf("viper %s: --%s needs a whole number: %s", name, flag, v)
	}
	return nil
}

// setValue stores the value of an option that takes one.
func setValue(o *options, name, flag, v string) error {
	switch flag {
	case "repo":
		o.repo = v
	case "cmd":
		o.cmd = v
	case "dir":
		o.dir = v
	case "arch":
		o.arch = v
	case "location":
		o.location = v
	case "provider":
		o.provider = v
	case "vat":
		return setVAT(o, name, v)
	case "group":
		o.group = v
	case "to", "topic", "from", "timeout", "tag", "k3d":
		return setNatsValue(o, name, flag, v)
	case "parallel":
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 64 {
			return fmt.Errorf("viper %s: --parallel needs a whole number from 1 to 64: %s", name, v)
		}
		o.parallel = n
	default:
		return setNumber(o, name, flag, v)
	}
	return nil
}

// setNatsValue stores the value of an option of viper nats and viper image.
func setNatsValue(o *options, name, flag, v string) error {
	switch flag {
	case "to":
		o.to = v
	case "topic": // more than once, or a list with commas: viper nats listen --topic build --topic news
		if o.topic != "" {
			v = o.topic + "," + v
		}
		o.topic = v
	case "from":
		o.from = v
	case "tag":
		o.tag = v
	case "k3d":
		o.k3d = v
	default:
		return setTimeout(o, name, v)
	}
	return nil
}

// setTimeout reads --timeout: seconds ("30") or a duration ("30s", "2m").
func setTimeout(o *options, name, v string) error {
	d, err := time.ParseDuration(v)
	if err != nil {
		n, nerr := strconv.Atoi(v)
		if nerr != nil {
			return fmt.Errorf("viper %s: --timeout needs seconds or a duration such as 30s: %s", name, v)
		}
		d = time.Duration(n) * time.Second
	}
	if d <= 0 {
		return fmt.Errorf("viper %s: --timeout must be positive: %s", name, v)
	}
	o.timeout = d
	return nil
}

// setVAT reads --vat: a percentage from 0 (prices without VAT) to 100.
func setVAT(o *options, name, v string) error {
	x, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(x) || x < 0 || x > 100 {
		return fmt.Errorf("viper %s: --vat needs a percentage between 0 and 100: %s", name, v)
	}
	o.vat, o.hasVAT = x, true
	return nil
}
