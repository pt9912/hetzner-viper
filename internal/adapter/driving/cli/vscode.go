package cli

import (
	"fmt"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

// remoteWorkdir asks the machine for its work directory (viper up wrote it there); empty if it
// cannot be read, e.g. on a machine set up by an older viper.
func remoteWorkdir(o *options, c *model.Machine) string {
	out, err := o.host().OutputTool("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-F", c.SSHConfig, c.SSHAlias, "cat ~/.config/viper/workdir")
	dir := strings.TrimSpace(out)
	if err != nil || !strings.HasPrefix(dir, "/") {
		return ""
	}
	return dir
}

// openVSCode opens VS Code on a running machine, in its work directory, like viper up does. The
// terminals there attach to the tmux session of the machine.
func openVSCode(o *options, env settings.Env) error {
	c, err := o.host().Services.Settings.Load(settingsDir(o), o.machine, env, false)
	if err != nil {
		return err
	}
	if !exists(c.SSHConfig) {
		return fmt.Errorf("No SSH config for %s: run viper up %s first", c.ServerName, o.machine)
	}
	if err := o.host().need("ssh"); err != nil {
		return err
	}
	return o.host().Services.LaunchVSCode(c.SSHAlias, remoteWorkdir(o, c))
}

func nativeVSCode(o *options) error {
	env, err := o.host().Env()
	if err != nil {
		return err
	}
	return openVSCode(o, env)
}
