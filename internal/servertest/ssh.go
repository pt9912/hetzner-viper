package servertest

import (
	"io"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/sshconfig"
	"github.com/pt9912/hetzner-viper/internal/model"
)

// SSH is the local ssh side of the machines for tests: writing the config and editing
// ~/.ssh/config go to the hooks (nil: done, nothing written); pins are the real files in the
// machine's directories.
type SSH struct {
	OnWrite   func(m *model.Machine, ip string) error
	OnInclude func(m *model.Machine, action string) error
}

// WriteConfig calls the hook.
func (s SSH) WriteConfig(m *model.Machine, ip string) error {
	if s.OnWrite == nil {
		return nil
	}
	return s.OnWrite(m, ip)
}

// AddInclude calls the hook with "add".
func (s SSH) AddInclude(m *model.Machine) error { return s.include(m, "add") }

// RemoveInclude calls the hook with "remove".
func (s SSH) RemoveInclude(m *model.Machine) error { return s.include(m, "remove") }

func (s SSH) include(m *model.Machine, action string) error {
	if s.OnInclude == nil {
		return nil
	}
	return s.OnInclude(m, action)
}

// IncludeWarning is the real warning.
func (SSH) IncludeWarning(m *model.Machine) string { return sshconfig.Files{}.IncludeWarning(m) }

// ForgetStalePin is the real one.
func (SSH) ForgetStalePin(m *model.Machine, id string, out io.Writer) {
	sshconfig.Files{}.ForgetStalePin(m, id, out)
}

// PinHostKey is the real one.
func (SSH) PinHostKey(m *model.Machine, id string, ssh func(args ...string) (string, error), out io.Writer) {
	sshconfig.Files{}.PinHostKey(m, id, ssh, out)
}
