// Package sshconfig is the driven port to the ssh files on this computer: the config viper writes
// for each machine, the line that includes it in ~/.ssh/config, and the pin of the host key. An
// adapter (internal/adapter/driven/sshconfig) keeps them in files.
package sshconfig

import (
	"io"

	"github.com/pt9912/hetzner-viper/internal/model"
)

// Store is the local ssh side of the machines.
type Store interface {
	// WriteConfig writes the machine's own ssh config, connecting to ip.
	WriteConfig(m *model.Machine, ip string) error
	// AddInclude and RemoveInclude add and remove the line that includes that config in
	// ~/.ssh/config. An error means the file cannot be edited; IncludeWarning says what to tell
	// the user then.
	AddInclude(m *model.Machine) error
	RemoveInclude(m *model.Machine) error
	IncludeWarning(m *model.Machine) string
	// ForgetStalePin drops a pinned host key recorded for another server ID: the name was reused
	// by a new server, e.g. created from another computer.
	ForgetStalePin(m *model.Machine, id string, out io.Writer)
	// PinHostKey pins the host key of the server if there is no pin yet: ssh runs a command on the
	// server over the connection that the host CA verified.
	PinHostKey(m *model.Machine, id string, ssh func(args ...string) (string, error), out io.Writer)
}
