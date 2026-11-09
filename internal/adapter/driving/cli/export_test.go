package cli

import (
	"context"
	"errors"
	"io"
	"sort"
	"sync"

	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

// What the external tests (package cli_test) may reach of the command line's internals.

// Opts are the options of a command.
type Opts struct {
	Machine, Repo, Cmd, Dir   string
	Group                     string
	NoJitter                  bool
	Parallel                  int
	Force, Full, NoCode, Tmux bool
	CPU, Disk                 int
	RAM                       float64
	Arch, Location, Provider  string
	VAT                       float64
	HasVAT                    bool
	Host                      *Host
}

func (p *Opts) options() *options {
	return &options{cpu: p.CPU, disk: p.Disk, ram: p.RAM, arch: p.Arch, location: p.Location, provider: p.Provider, vat: p.VAT, hasVAT: p.HasVAT, noJitter: p.NoJitter, group: p.Group, parallel: p.Parallel, machine: p.Machine, repo: p.Repo, cmd: p.Cmd, dir: p.Dir, force: p.Force, full: p.Full, noCode: p.NoCode, tmux: p.Tmux, h: p.Host}
}

// Parse reads the command line.
func Parse(args []string) (string, Opts, error) {
	name, o, err := parse(args)
	return name, Opts{NoJitter: o.noJitter, Group: o.group, Parallel: o.parallel, Machine: o.machine, Repo: o.repo, Cmd: o.cmd, Dir: o.dir, Force: o.force, Full: o.full, NoCode: o.noCode, Tmux: o.tmux, CPU: o.cpu, Disk: o.disk, RAM: o.ram, Arch: o.arch, Location: o.location, Provider: o.provider, VAT: o.vat, HasVAT: o.hasVAT, Host: o.h}, err
}

// IsHelp reports whether the error is the request for the help text.
func IsHelp(err error) bool { return errors.Is(err, errHelp) }

// HelpText is the text of viper help.
func HelpText(version string) string { return helpText(version) }

// ExitCode is the exit code of an error.
func ExitCode(err error) int { return exitCode(err) }

// CommandNames are the names of the commands.
func CommandNames() []string {
	var names []string
	for n := range commands() {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// RunCommand runs the command of that name with the options.
func RunCommand(name string, p *Opts) error { return commands()[name].run(p.options()) }

// NewAsker reads the answers to prompts from in.
func NewAsker(ctx context.Context, in io.Reader, prompts io.Writer) func(string) string {
	return newAsker(ctx, in, prompts)
}

// RunFind runs viper find with a given environment.
func RunFind(ctx context.Context, p *Opts, env settings.Env, stdout, stderr io.Writer) error {
	return runFind(ctx, p.options(), env, stdout, stderr)
}

// RunUp, RunDown, RunCheck and RunPreflight run the commands with a given environment.
func RunUp(ctx context.Context, p *Opts, env settings.Env, stdout, stderr io.Writer) error {
	return runUp(ctx, p.options(), env, stdout, stderr)
}

// RunDown runs viper down.
func RunDown(ctx context.Context, p *Opts, env settings.Env, in io.Reader, stdout, stderr io.Writer) error {
	return runDown(ctx, p.options(), env, in, stdout, stderr)
}

// RunCheck runs viper check.
func RunCheck(ctx context.Context, p *Opts, env settings.Env, stdout, stderr io.Writer) error {
	return runCheck(ctx, p.options(), env, stdout, stderr)
}

// RunPreflight runs viper preflight.
func RunPreflight(ctx context.Context, p *Opts, env settings.Env, stdout, stderr io.Writer) error {
	return runPreflight(ctx, p.options(), env, stdout, stderr)
}

// Setup runs viper setup.
func Setup(p *Opts, env settings.Env, out io.Writer) error { return setup(p.options(), env, out) }

// Connect runs viper ssh.
func Connect(p *Opts, env settings.Env, interactive bool) error {
	return connect(p.options(), env, interactive)
}

// ShowLog runs viper log.
func ShowLog(p *Opts, env settings.Env, out io.Writer) error { return showLog(p.options(), env, out) }

// DNSName runs viper dns.
func DNSName(p *Opts, env settings.Env, out io.Writer) error { return dnsName(p.options(), env, out) }

// OpenVSCode runs viper vscode.
func OpenVSCode(p *Opts, env settings.Env) error { return openVSCode(p.options(), env) }

// IsNative reports whether the command of that name is implemented in Go.
func IsNative(name string) bool { return commands()[name].run != nil }

// ManualLines are the lines of the help text that name the manual.
func ManualLines(version string) string { return manualLines(version) }

// TmuxCommand is what viper ssh --tmux runs on the machine.
const TmuxCommand = tmuxCommand

// MachineRun is the command for one machine of a group.
type MachineRun func(ctx context.Context, p *Opts, env settings.Env, out, errOut io.Writer) error

// RunGroup runs a command for the machines of a group.
func RunGroup(ctx context.Context, p *Opts, env settings.Env, stdout, stderr io.Writer, run MachineRun) error {
	return runGroup(ctx, p.options(), env, stdout, stderr, func(ctx context.Context, o *options, env settings.Env, out, errOut io.Writer) error {
		q := *p
		q.Machine, q.Group = o.machine, ""
		return run(ctx, &q, env, out, errOut)
	})
}

// ExitStatus is the error of a command that ended with that code.
func ExitStatus(code int) error { return exitStatus(code) }

// NewPrefixWriter writes the lines of a machine with its name in front, under a lock of its own.
func NewPrefixWriter(out io.Writer, machine string, width int) (w io.Writer, flush func()) {
	pw := newPrefixWriter(&sync.Mutex{}, out, machine, width)
	return pw, pw.flush
}

// NewPrefixWriters makes the writers of several machines that share one lock.
func NewPrefixWriters(out io.Writer, width int, machines ...string) ([]io.Writer, func()) {
	mu := &sync.Mutex{}
	var ws []io.Writer
	var all []*prefixWriter
	for _, m := range machines {
		pw := newPrefixWriter(mu, out, m, width)
		ws, all = append(ws, pw), append(all, pw)
	}
	return ws, func() {
		for _, pw := range all {
			pw.flush()
		}
	}
}

// RunGroupUp starts the machines of a group.
func RunGroupUp(ctx context.Context, p *Opts, env settings.Env, stdout, stderr io.Writer) error {
	return runGroupUp(ctx, p.options(), env, stdout, stderr)
}
