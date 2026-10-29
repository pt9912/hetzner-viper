package cli

import (
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
	Settings  settings.Store
	Up        func(c *model.Machine, o driving.UpOptions) driving.Up
	Down      func(c *model.Machine, o driving.DownOptions) driving.Down
	Check     func(c *model.Machine) driving.Check
	Preflight func(c *model.Machine) driving.Preflight
	// Find searches the price lists of the providers; get reads a setting (the credentials).
	Find func(get func(string) string) driving.Find
	// OpenLog starts the log of a run, ShowLog shows the log of a server (viper log).
	OpenLog runlog.Opener
	ShowLog runlog.Viewer
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
	LookPath   func(file string) (string, error)
	// Env is the home directory and the environment.
	Env func() (settings.Env, error)
	// IsTerminal reports whether stdin and stdout are a terminal (an interactive ssh session).
	IsTerminal func() bool

	Stdin          io.Reader
	Stdout, Stderr io.Writer

	Services Services
}
