package cli

import (
	"os/exec"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/runlog"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

// Services are the use cases the commands call, made for one machine: the driving ports. The
// composition root (internal/app) gives the real ones, the tests others.
type Services struct {
	Up        func(c *model.Machine, o driving.UpOptions) driving.Up
	Down      func(c *model.Machine, o driving.DownOptions) driving.Down
	Check     func(c *model.Machine) driving.Check
	Preflight func(c *model.Machine) driving.Preflight
	// OpenLog starts the log of a run, ShowLog shows the log of a server (viper log).
	OpenLog runlog.Opener
	ShowLog runlog.Viewer
	// LaunchVSCode opens VS Code on a machine (viper vscode).
	LaunchVSCode func(alias, workdir string) error
}

// host is what the commands use of the outside world: the programs they run and the use cases.
// The tests replace them.
type host struct {
	runTool    func(name string, args ...string) error
	execTool   func(name string, args ...string) error
	outputTool func(name string, args ...string) (string, error)
	lookPath   func(file string) (string, error)
	services   Services
}

func defaultHost() *host {
	return &host{
		runTool:    runChild,
		execTool:   replaceProcess,
		outputTool: outputOf,
		lookPath:   exec.LookPath,
	}
}
