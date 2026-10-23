package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/pt9912/hetzner-viper/internal/cloudflare"
	"github.com/pt9912/hetzner-viper/internal/config"
	"github.com/pt9912/hetzner-viper/internal/hetzner"
	"github.com/pt9912/hetzner-viper/internal/machine"
	"github.com/pt9912/hetzner-viper/internal/runlog"
	"github.com/pt9912/hetzner-viper/internal/stop"
)

// replaced by the tests
var newDownDeps = func(c *config.Config) stop.Deps {
	cf := &cloudflare.Client{Token: c.Get("CLOUDFLARE_API_TOKEN"), ZoneID: c.Get("CLOUDFLARE_ZONE_ID")}
	return stop.Deps{
		Hetzner:        hetzner.New(c.Get("HCLOUD_TOKEN"), ""),
		DNS:            cf,
		Run:            runCaptured,
		WriteSSHConfig: func(ip string) error { return machine.WriteSSHConfig(c, ip) },
		SSHInclude:     func(action string) error { return machine.SSHInclude(c, action) },
	}
}

// runDown is stop.sh: the checks, the confirmations and the deletion. Answers are read from in
// (a terminal or a pipe); prompts and reports go to stderr, like the script's.
func runDown(ctx context.Context, o *options, env config.Env, in io.Reader, stdout, stderr io.Writer) error {
	c, err := config.Load(settingsDir(o), o.machine, env, true)
	if err != nil {
		return err
	}
	l, err := runlog.Open(c.StateDir, c.ServerName, "down", stdout, stderr)
	if err != nil {
		return err
	}
	ctx, stop_ := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop_()

	deps := newDownDeps(c)
	lines := make(chan string)
	go func() { // one reader for all prompts: answers that follow in a pipe must not be lost
		sc := bufio.NewScanner(in)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
		close(lines)
	}()
	deps.Ask = func(prompt string) string {
		fmt.Fprint(l.Err, prompt)
		select {
		case line, ok := <-lines:
			if !ok {
				return ""
			}
			return strings.TrimRight(line, "\r")
		case <-ctx.Done():
			return "" // never confirms anything
		}
	}
	code, err := stop.Run(ctx, c, o.force, deps, l, l.Out, l.Err)
	switch {
	case ctx.Err() != nil:
		fmt.Fprintln(l.Err)
		l.Interrupted()
		l.Close(ctx.Err(), 130)
		return exitStatus(130)
	case err != nil && !errors.Is(err, stop.ErrShown):
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

func nativeDown(o *options) error {
	env, err := hostEnv()
	if err != nil {
		return err
	}
	return runDown(context.Background(), o, env, os.Stdin, os.Stdout, os.Stderr)
}
