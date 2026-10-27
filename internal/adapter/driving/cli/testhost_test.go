package cli

import (
	"io"

	runlogfile "github.com/pt9912/hetzner-viper/internal/adapter/driven/runlog"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/runlog"
)

// testHost is the real programs of defaultHost and the file log in the machine's state directory.
func testHost() *host {
	h := defaultHost()
	h.services.OpenLog = func(m *model.Machine, action string, stdout, stderr io.Writer) (runlog.Run, error) {
		l, err := runlogfile.Open(m.StateDir, m.ServerName, action, stdout, stderr)
		if err != nil {
			return nil, err
		}
		return l, nil
	}
	h.services.ShowLog = runlogfile.Show
	return h
}
