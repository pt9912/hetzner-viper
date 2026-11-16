package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

// nativeTerminal is viper terminal: a window of the desktop with the tmux session of the machine, or with --here the same in this
// terminal.
func nativeTerminal(o *options) error {
	env, err := o.env()
	if err != nil {
		return err
	}
	if o.here {
		return terminalHere(o, env)
	}
	c, err := o.host().Services.Settings.Load(settingsDir(o), o.machine, env, false)
	if err != nil {
		return err
	}
	if !exists(c.SSHConfig) {
		return fmt.Errorf("No SSH config for %s: run viper up %s first", c.ServerName, o.machine)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir, err := filepath.Abs(settingsDir(o)) // the window starts in another directory
	if err != nil {
		return err
	}
	return o.host().Services.LaunchTerminal(machineLabel(o.machine, c.Group), terminalWords(exe, o.machine, o.within, dir))
}

// terminalWords is the program of the window: viper terminal NAME [--in-group G] --here --dir DIR, every word as it is (the
// terminal program gets them one by one).
func terminalWords(exe, machine, group, dir string) []string {
	words := []string{exe, "terminal", machine}
	if group != "" {
		words = append(words, "--in-group", group)
	}
	return append(words, "--here", "--dir", dir)
}

// terminalHere runs viper ssh --tmux in this terminal. ssh is a child, so that viper can wait for it: when it fails, the message
// stays and the window waits for Enter (a window that closes at once shows nothing).
func terminalHere(o *options, env settings.Env) error {
	o.tmux, o.cmd = true, ""
	interactive := o.host().IsTerminal()
	if interactive { // the title of the window (the terminal programs know this sequence)
		fmt.Fprintf(o.host().Stdout, "\x1b]0;%s\x07", machineLabel(o.machine, o.within))
	}
	err := connectAs(o, env, interactive, true)
	if err == nil || !interactive {
		return err
	}
	fmt.Fprintf(o.host().Stderr, "viper: %v\nPress Enter to close this window.\n", err)
	_, _ = bufio.NewReader(o.host().Stdin).ReadString('\n')
	return err
}
