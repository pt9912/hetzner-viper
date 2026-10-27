package cli_test

import (
	"io"
	"os"
	"strings"

	runlogfile "github.com/pt9912/hetzner-viper/internal/adapter/driven/runlog"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/settingsfile"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/system"
	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/runlog"
)

// testHost is the real programs, the settings files and the file log in the machine's state
// directory, with a terminal that is none and streams that go nowhere.
func testHost() *cli.Host {
	return &cli.Host{
		RunTool: func(name string, args ...string) error {
			return system.RunAttached(nil, io.Discard, io.Discard, name, args...)
		},
		ExecTool: func(name string, args ...string) error {
			return system.Exec(nil, io.Discard, io.Discard, name, args...)
		},
		OutputTool: system.Output,
		LookPath:   system.LookPath,
		Env:        system.HostEnvironment,
		IsTerminal: func() bool { return false },
		Stdin:      strings.NewReader(""),
		Stdout:     io.Discard,
		Stderr:     io.Discard,
		Services: cli.Services{
			Settings: settingsfile.Files{},
			OpenLog: func(m *model.Machine, action string, stdout, stderr io.Writer) (runlog.Run, error) {
				l, err := runlogfile.Open(m.StateDir, m.ServerName, action, stdout, stderr)
				if err != nil {
					return nil, err
				}
				return l, nil
			},
			ShowLog: runlogfile.Show,
		},
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }
