package cli

import (
	"context"
	"errors"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/chatweb"
	"github.com/pt9912/hetzner-viper/internal/adapter/driving/web"
	"github.com/pt9912/hetzner-viper/internal/port/driven/chatstream"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

// What the external tests (package cli_test) may reach of the command line's internals.

// Opts are the options of a command.
type Opts struct {
	Machine, Repo, Cmd, Dir      string
	Group                        string
	NoJitter                     bool
	Parallel                     int
	Force, Full, NoCode, Tmux    bool
	CPU, Disk                    int
	RAM                          float64
	Arch, Location, Provider     string
	VAT                          float64
	HasVAT                       bool
	Host                         *Host
	Args                         []string
	To, Topic, From, Name, File  string
	PDF, Image, Zip, SHA256, Out string
	Timeout                      time.Duration
	Port                         int
	Query, All, JSON, Yes        bool
	NoPresence, HostBin          bool
	PrintConfig                  bool
	PushFrom, Files              string
	Registry                     string
	Within                       string
}

func (p *Opts) options() *options {
	o := &options{cpu: p.CPU, disk: p.Disk, ram: p.RAM, arch: p.Arch, location: p.Location, provider: p.Provider, vat: p.VAT, hasVAT: p.HasVAT, noJitter: p.NoJitter, group: p.Group, parallel: p.Parallel, machine: p.Machine, repo: p.Repo, cmd: p.Cmd, dir: p.Dir, force: p.Force, full: p.Full, noCode: p.NoCode, tmux: p.Tmux, h: p.Host, args: p.Args, to: p.To, topic: p.Topic, from: p.From, name: p.Name, file: p.File, pdf: p.PDF, image: p.Image, zip: p.Zip, sha256: p.SHA256, out: p.Out, timeout: p.Timeout, query: p.Query, all: p.All, asJSON: p.JSON, yes: p.Yes, noPresence: p.NoPresence, hostBin: p.HostBin, port: p.Port, printConfig: p.PrintConfig, pushFrom: p.PushFrom, files: p.Files} //nolint:dupl // the two sides of the same conversion
	o.registry, o.within = p.Registry, p.Within
	return o
}

// Parse reads the command line.
func Parse(args []string) (string, Opts, error) {
	name, o, err := parse(args)
	p := Opts{NoJitter: o.noJitter, Group: o.group, Parallel: o.parallel, Machine: o.machine, Repo: o.repo, Cmd: o.cmd, Dir: o.dir, Force: o.force, Full: o.full, NoCode: o.noCode, Tmux: o.tmux, CPU: o.cpu, Disk: o.disk, RAM: o.ram, Arch: o.arch, Location: o.location, Provider: o.provider, VAT: o.vat, HasVAT: o.hasVAT, Host: o.h, Args: o.args, To: o.to, Topic: o.topic, From: o.from, Name: o.name, File: o.file, PDF: o.pdf, Image: o.image, Zip: o.zip, SHA256: o.sha256, Out: o.out, Timeout: o.timeout, Query: o.query, All: o.all, JSON: o.asJSON, Yes: o.yes, NoPresence: o.noPresence, HostBin: o.hostBin, Port: o.port, PrintConfig: o.printConfig, PushFrom: o.pushFrom, Files: o.files} //nolint:dupl // the two sides of the same conversion
	p.Registry, p.Within = o.registry, o.within
	return name, p, err
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

// TmuxCommand is what viper ssh --tmux runs on the machine, for the label of the status line.
func TmuxCommand(label string) string { return tmuxCommand(label) }

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

// RunGroupDown deletes the machines of a group; in has the answers.
func RunGroupDown(ctx context.Context, p *Opts, env settings.Env, in io.Reader, stdout, stderr io.Writer) error {
	return runGroupDown(ctx, p.options(), env, in, stdout, stderr)
}

// RunGroupSSH runs a command on the machines of a group.
func RunGroupSSH(ctx context.Context, p *Opts, env settings.Env, stdout, stderr io.Writer) error {
	return runGroup(ctx, p.options(), env, stdout, stderr, sshOnce)
}

// CommandFlags are the options of a command besides --dir.
func CommandFlags(name string) []string { return commands()[name].flags }

// HelpOptionNames are the options a command's help describes besides --dir.
func HelpOptionNames(name string) []string {
	var out []string
	for _, o := range helps()[name].options {
		out = append(out, o.name)
	}
	return out
}

// NatsSubNames are the subcommands of viper nats with a help of their own.
func NatsSubNames() []string {
	var out []string
	for n := range natsSubs() {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// NatsSubOptionNames are the options a subcommand of viper nats describes.
func NatsSubOptionNames(sub string) []string { return natsSubs()[sub].options }

// CommandHelp is the help of one command.
func CommandHelp(name string) string { return commandHelp(name, "v9.9.9") }

// NatsSubHelp is the help of one subcommand of viper nats.
func NatsSubHelp(sub string) string { return natsSubHelp(sub, "v9.9.9") }

// UIBackend is what viper ui shows, for the options (the directory) and the environment, with the redaction of its pages.
func UIBackend(p *Opts, env settings.Env) (*uiBackend, func(string) string) {
	secrets := &secretValues{}
	b := &uiBackend{o: p.options(), env: env, secrets: secrets}
	b.learnEnvironment()
	return b, secrets.redact
}

// BrowserCommand is the program that opens a file in the browser on a system.
func BrowserCommand(goos, file string) (string, []string) { return browserCommand(goos, file) }

// UISettings are the settings files viper ui lists, with the environment given.
func UISettings(p *Opts, env settings.Env) ([]web.SettingsFile, error) {
	b := &uiBackend{o: p.options(), env: env, secrets: &secretValues{}}
	return b.Settings(context.Background())
}

// StartProcess starts a program as viper ui starts viper.
func StartProcess(exe string, args ...string) (*process, error) { return startProcess(exe, args) }

// UIEditor is what viper ui --edit does, for the options (the directory) and the environment.
func UIEditor(p *Opts, env settings.Env) (*uiEditor, func(string) string) {
	secrets := &secretValues{}
	return &uiEditor{b: &uiBackend{o: p.options(), env: env, secrets: secrets}}, secrets.redact
}

// CheckRemoteCommand is checkRemoteCommand for the tests.
func CheckRemoteCommand(cmd string) error { return checkRemoteCommand(cmd) }

// NativeSSH runs viper ssh (the checks of its command line before it connects).
func NativeSSH(p *Opts) error { return nativeSSH(p.options()) }

// Terminal runs viper terminal.
func Terminal(p *Opts, env settings.Env) error { _ = env; return nativeTerminal(p.options()) }

// TerminalWords is terminalWords for the tests.
func TerminalWords(exe, machine, group, dir string) []string {
	return terminalWords(exe, machine, group, dir)
}

// TerminalHere runs viper terminal --here.
func TerminalHere(p *Opts, env settings.Env) error { return terminalHere(p.options(), env) }

// ChatBackend and ChatConnector are the parts of viper ui-chat that speak to a NATS group.
func ChatBackend(p *Opts, env settings.Env) chatweb.Backend { //nolint:ireturn // the contract of the chat
	return &chatBackend{b: &uiBackend{o: p.options(), env: env, secrets: &secretValues{}}}
}

// ChatConnector is the connection of the stream.
func ChatConnector(p *Opts, env settings.Env) chatstream.Connector { //nolint:ireturn // the contract of the chat
	return &chatConnector{o: p.options(), env: env}
}
