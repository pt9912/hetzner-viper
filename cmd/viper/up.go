package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/pt9912/hetzner-viper/internal/cloudflare"
	"github.com/pt9912/hetzner-viper/internal/config"
	"github.com/pt9912/hetzner-viper/internal/hetzner"
	"github.com/pt9912/hetzner-viper/internal/machine"
	"github.com/pt9912/hetzner-viper/internal/preflight"
	"github.com/pt9912/hetzner-viper/internal/runlog"
	"github.com/pt9912/hetzner-viper/internal/start"
)

// runStream runs a program with its output going to the user as it comes.
func runStream(ctx context.Context, stdin string, stdout, stderr io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 5 * time.Second
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	return cmd.Run()
}

// vscodeEnv is the environment VS Code and its extensions get: without the API tokens of the
// settings, which they must not inherit.
func vscodeEnv(environ []string) []string {
	drop := map[string]bool{
		"HCLOUD_TOKEN": true, "CLOUDFLARE_API_TOKEN": true, "CLOUDFLARE_ZONE_ID": true, "VIPER_GH_TOKEN": true, "GH_TOKEN": true,
		"VIPER_CLAUDE_TOKEN": true, "CLAUDE_CODE_OAUTH_TOKEN": true, "VIPER_ANTHROPIC_AUTH_TOKEN": true, "VIPER_CODE_SERVER_PASSWORD": true,
		"VIPER_TRANSCRIPT": true, "VIPER_TRANSCRIPT_PID": true, "VIPER_ACTION": true,
	}
	var out []string
	for _, kv := range environ {
		if name, _, _ := strings.Cut(kv, "="); !drop[name] {
			out = append(out, kv)
		}
	}
	return out
}

// replaced by the tests
var newUpDeps = func(c *config.Config, preflightDeps func() preflight.Deps) start.Deps {
	return start.Deps{
		Preflight: func(ctx context.Context, repoArg string, out, errOut io.Writer) int {
			return preflight.Run(ctx, c, repoArg, preflightDeps(), out, errOut)
		},
		Hetzner: hetzner.New(c.Get("HCLOUD_TOKEN"), ""),
		DNS:     &cloudflare.Client{Token: c.Get("CLOUDFLARE_API_TOKEN"), ZoneID: c.Get("CLOUDFLARE_ZONE_ID")},
		Run:     runCaptured,
		Stream:  runStream,
		Keygen: func(name string, args ...string) error {
			out, err := exec.Command(name, args...).CombinedOutput()
			if err != nil {
				return &keygenError{err, strings.TrimSpace(string(out))}
			}
			return nil
		},
		WriteSSHConfig: func(ip string) error { return machine.WriteSSHConfig(c, ip) },
		SSHInclude:     func(action string) error { return machine.SSHInclude(c, action) },
		Sleep:          time.Sleep,
		Now:            time.Now,
		OpenVSCode: func(alias, workdir string) {
			path, err := exec.LookPath("code")
			if err != nil {
				return
			}
			cmd := exec.Command(path, "--remote", "ssh-remote+"+alias, workdir)
			cmd.Env = vscodeEnv(os.Environ())
			_ = cmd.Run()
		},
	}
}

type keygenError struct {
	err error
	out string
}

func (e *keygenError) Error() string {
	if e.out != "" {
		return e.err.Error() + ": " + e.out
	}
	return e.err.Error()
}

// runUp is start.sh: create or reuse the machine and set it up.
func runUp(ctx context.Context, o *options, env config.Env, stdout, stderr io.Writer) error {
	c, err := config.Load(settingsDir(o), o.machine, env, true)
	if err != nil {
		return err
	}
	l, err := runlog.Open(c.StateDir, c.ServerName, "up", stdout, stderr)
	if err != nil {
		return err
	}
	ctx, stop := notifyContext(ctx)
	defer stop()
	deps := newUpDeps(c, func() preflight.Deps { return newPreflightDeps(c) })
	// the pauses end with an interruption
	deps.Sleep = func(d time.Duration) {
		select {
		case <-time.After(d):
		case <-ctx.Done():
		}
	}
	code, err := start.Run(ctx, c, o.repo, deps, l, l.Out, l.Err)
	switch {
	case ctx.Err() != nil:
		l.Interrupted()
		l.Close(ctx.Err(), 130)
		return exitStatus(130)
	case err != nil && err != start.ErrShown:
		l.Close(err, 1)
		return err
	case err != nil:
		l.Close(err, code)
		return exitStatus(code)
	}
	l.Close(nil, code)
	if code != 0 {
		return exitStatus(code)
	}
	return nil
}

func nativeUp(o *options) error {
	// before anything is created: a program that is missing later would leave a server behind
	for _, program := range []string{"ssh", "ssh-keygen", "scp"} {
		if err := need(program); err != nil {
			return err
		}
	}
	env, err := hostEnv()
	if err != nil {
		return err
	}
	return runUp(context.Background(), o, env, os.Stdout, os.Stderr)
}
