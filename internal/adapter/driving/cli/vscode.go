package cli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/config"
	"github.com/pt9912/hetzner-viper/internal/model"
)

// outputOf runs a program and returns what it wrote to its standard output.
func outputOf(name string, args ...string) (string, error) {
	out, err := exec.CommandContext(context.Background(), name, args...).Output()
	return string(out), err
}

// remoteWorkdir asks the machine for its work directory (viper up wrote it there); empty if it
// cannot be read, e.g. on a machine set up by an older viper.
func remoteWorkdir(o *options, c *model.Machine) string {
	out, err := o.host().outputTool("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-F", c.SSHConfig, c.SSHAlias, "cat ~/.config/viper/workdir")
	dir := strings.TrimSpace(out)
	if err != nil || !strings.HasPrefix(dir, "/") {
		return ""
	}
	return dir
}

// openVSCode opens VS Code on a running machine, in its work directory, like viper up does. The
// terminals there attach to the tmux session of the machine.
func openVSCode(o *options, env config.Env) error {
	c, err := config.Load(settingsDir(o), o.machine, env, false)
	if err != nil {
		return err
	}
	if !exists(c.SSHConfig) {
		return fmt.Errorf("No SSH config for %s: run viper up %s first", c.ServerName, o.machine)
	}
	if err := o.host().need("ssh"); err != nil {
		return err
	}
	return o.host().services.LaunchVSCode(c.SSHAlias, remoteWorkdir(o, c))
}

func nativeVSCode(o *options) error {
	env, err := hostEnv()
	if err != nil {
		return err
	}
	return openVSCode(o, env)
}
