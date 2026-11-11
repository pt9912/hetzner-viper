package cli

import (
	"context"
	"github.com/pt9912/hetzner-viper/internal/port/driven/system"
	"io"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/runlog"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

// Services are the use cases the commands call, made for one machine: the driving ports. The
// composition root (internal/app) gives the real ones, the tests others.
type Services struct {
	// Settings reads the settings of the machines.
	Settings settings.Store
	// Groups runs a command for the machines of a group at the same time (--group).
	Groups driving.GroupRunner
	// NATS speaks to a NATS group as one of its machines (viper nats).
	NATS driving.NATSCLI
	// NATSOp registers the operator of a NATS group at its server (viper nats init).
	NATSOp    driving.NATSOperator
	Up        func(c *model.Machine, o driving.UpOptions) driving.Up
	Down      func(c *model.Machine, o driving.DownOptions) driving.Down
	Check     func(c *model.Machine) driving.Check
	Preflight func(c *model.Machine) driving.Preflight
	// Find searches the price lists of the providers; get reads a setting (the credentials), notice
	// is where it says what takes long.
	Find func(get func(string) string, notice io.Writer) driving.Find
	// OpenLog starts the log of a run, ShowLog shows the log of a server (viper log).
	OpenLog runlog.Opener
	ShowLog runlog.Viewer
	// MCPBinary is the viper-nats-mcp program built into viper for a Linux architecture ("amd64" or "arm64").
	MCPBinary func(goarch string) ([]byte, error)
	// LaunchVSCode opens VS Code on a machine (viper vscode).
	LaunchVSCode func(alias, workdir string) error
}

// Host is what the commands use of the outside world: the programs they run, the terminal and
// the environment, and the use cases. The composition root (internal/app) gives the real ones;
// the tests give others.
type Host struct {
	// RunTool runs a program on the terminal, ExecTool replaces viper by one, OutputTool returns
	// what one prints, LookPath finds one.
	RunTool    func(name string, args ...string) error
	ExecTool   func(name string, args ...string) error
	OutputTool func(name string, args ...string) (string, error)
	// StreamTool runs a program with its output going to the writers as it comes; it ends with ctx
	// (the group commands use it; without it they fall back to OutputTool).
	StreamTool system.Streamer
	// OpenTunnel makes host:port of the NATS server reachable on this computer through ssh to the machine
	// and returns the local address and a function that closes it (viper nats as the operator). nil: the
	// default over ssh -L with StreamTool.
	OpenTunnel func(ctx context.Context, via driving.NATSTarget, host string, port int) (addr string, stop func(), err error)
	LookPath   func(file string) (string, error)
	// Env is the home directory and the environment.
	Env func() (settings.Env, error)
	// IsTerminal reports whether stdin and stdout are a terminal (an interactive ssh session).
	IsTerminal func() bool

	Stdin          io.Reader
	Stdout, Stderr io.Writer

	Services Services
}
