package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/pt9912/hetzner-viper/internal/check"
	"github.com/pt9912/hetzner-viper/internal/config"
	"github.com/pt9912/hetzner-viper/internal/hetzner"
	"github.com/pt9912/hetzner-viper/internal/machine"
	"github.com/pt9912/hetzner-viper/internal/runlog"
)

// nameserver is the first resolver in /etc/resolv.conf, "" if there is none.
func nameserver() string {
	b, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "nameserver" {
			return f[1]
		}
	}
	return ""
}

// run runs a program with the given input and returns what it printed.
func runCaptured(ctx context.Context, stdin, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 5 * time.Second // a program that left children holding the pipes cannot hang us
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

// defaultCheckDeps are the real dependencies of check.
func defaultCheckDeps(c *config.Config) check.Deps {
	return check.Deps{
		Hetzner: hetzner.New(c.Get("HCLOUD_TOKEN"), ""),
		LookupIPv4: func(ctx context.Context, host string) ([]string, error) {
			ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
			out := make([]string, len(ips))
			for i, ip := range ips {
				out[i] = ip.String()
			}
			return out, err
		},
		Nameserver:     nameserver(),
		Run:            runCaptured,
		WriteSSHConfig: func(ip string) error { return machine.WriteSSHConfig(c, ip) },
	}
}

// runCheck is check.sh: read-only checks of a running machine, logged like the script's.
func runCheck(ctx context.Context, o *options, env config.Env, stdout, stderr io.Writer) error {
	c, err := config.Load(settingsDir(o), o.machine, env, true)
	if err != nil {
		return err
	}
	l, err := runlog.Open(c.StateDir, c.ServerName, "check", stdout, stderr)
	if err != nil {
		return err
	}
	ctx, stop := notifyContext(ctx)
	defer stop()
	failures, err := check.Run(ctx, c, o.host().newCheckDeps(c), l.Out, l.Err)
	switch {
	case ctx.Err() != nil:
		l.Interrupted()
		l.Close(ctx.Err(), 130)
		return exitStatus(130)
	case err != nil:
		// the reason was shown; without a final event the run is logged as FAILED
		l.Close(err, 1)
		return exitStatus(1)
	case failures > 0:
		l.Final("check: %d problem(s)", failures)
		l.Close(nil, 1)
		return exitStatus(1)
	}
	l.Final("check OK")
	l.Close(nil, 0)
	return nil
}

func nativeCheck(o *options) error {
	if err := o.host().need("ssh"); err != nil {
		return err
	}
	env, err := hostEnv()
	if err != nil {
		return err
	}
	return runCheck(context.Background(), o, env, os.Stdout, os.Stderr)
}
