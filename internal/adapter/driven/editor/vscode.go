// Package editor opens the editor on a machine: the adapter that starts VS Code with Remote-SSH.
package editor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/config"
)

// Env is the environment VS Code and its extensions get: without the API tokens of the
// settings, which they must not inherit.
func Env(environ []string) []string {
	drop := map[string]bool{
		"CLOUDFLARE_API_TOKEN": true, "CLOUDFLARE_ZONE_ID": true, "VIPER_GH_TOKEN": true, "GH_TOKEN": true,
		"VIPER_CLAUDE_TOKEN": true, "CLAUDE_CODE_OAUTH_TOKEN": true, "VIPER_ANTHROPIC_AUTH_TOKEN": true, "VIPER_CODE_SERVER_PASSWORD": true,
		"VIPER_TRANSCRIPT": true, "VIPER_TRANSCRIPT_PID": true, "VIPER_ACTION": true,
	}
	// the credentials of every provider, not only the one of this machine
	for _, name := range config.ProviderNames() {
		info, _ := config.Provider(name)
		for _, cred := range info.Credentials {
			drop[cred] = true
		}
	}
	var out []string
	for _, kv := range environ {
		if name, _, _ := strings.Cut(kv, "="); !drop[name] {
			out = append(out, kv)
		}
	}
	return out
}

// LaunchVSCode opens VS Code on the machine (Remote-SSH) in the work directory, or without a folder
// if workdir is empty; it waits for the `code` command, which returns once the window is requested.
func LaunchVSCode(alias, workdir string) error {
	path, err := exec.LookPath("code")
	if err != nil {
		return fmt.Errorf("code not found: install VS Code and its command line tool `code`")
	}
	args := []string{"--remote", "ssh-remote+" + alias}
	if workdir != "" {
		args = append(args, workdir)
	}
	cmd := exec.CommandContext(context.Background(), path, args...)
	cmd.Env = Env(os.Environ())
	return cmd.Run()
}
