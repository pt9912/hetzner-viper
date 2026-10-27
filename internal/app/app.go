// Package app is the composition root: it makes the adapters, plugs them into the use cases and
// gives the use cases to the command line. Nothing else knows which adapter is behind a port.
package app

import (
	"context"
	"io"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/cloudflare"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/editor"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/git"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/resolver"
	runlogfile "github.com/pt9912/hetzner-viper/internal/adapter/driven/runlog"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/settingsfile"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/sshconfig"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/system"
	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
	"github.com/pt9912/hetzner-viper/internal/port/driven/runlog"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
	"github.com/pt9912/hetzner-viper/internal/usecase/check"
	"github.com/pt9912/hetzner-viper/internal/usecase/preflight"
	"github.com/pt9912/hetzner-viper/internal/usecase/start"
	"github.com/pt9912/hetzner-viper/internal/usecase/stop"
)

// Run is viper: it runs the command in args with the real adapters and returns the exit code.
// version is the release the program was built from.
func Run(version string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return cli.Main(version, args, &cli.Host{
		RunTool: func(name string, args ...string) error {
			return system.RunAttached(stdin, stdout, stderr, name, args...)
		},
		ExecTool: func(name string, args ...string) error {
			return system.Exec(stdin, stdout, stderr, name, args...)
		},
		OutputTool: system.Output,
		LookPath:   system.LookPath,
		Env:        system.HostEnvironment,
		IsTerminal: func() bool { return system.IsTerminal(stdin) && system.IsTerminal(stdout) },
		Stdin:      stdin,
		Stdout:     stdout,
		Stderr:     stderr,
		Services: cli.Services{
			Settings:  settingsfile.Files{},
			Up:        func(c *model.Machine, o driving.UpOptions) driving.Up { return Up(c, o) },
			Down:      func(c *model.Machine, o driving.DownOptions) driving.Down { return Down(c, o) },
			Check:     func(c *model.Machine) driving.Check { return Check(c) },
			Preflight: func(c *model.Machine) driving.Preflight { return Preflight(c) },
			OpenLog: func(m *model.Machine, action string, stdout, stderr io.Writer) (runlog.Run, error) {
				l, err := runlogfile.Open(m.StateDir, m.ServerName, action, stdout, stderr)
				if err != nil {
					return nil, err
				}
				return l, nil
			},
			ShowLog:      runlogfile.Show,
			LaunchVSCode: vscode().Launch,
		},
	})
}

func dnsOf(c *model.Machine) *cloudflare.Client {
	return &cloudflare.Client{Token: c.Get("CLOUDFLARE_API_TOKEN"), ZoneID: c.Get("CLOUDFLARE_ZONE_ID")}
}

// Preflight is the use case Preflight for the machine of c.
func Preflight(c *model.Machine) preflight.Service {
	return preflightOf(providerOf(c), dnsOf(c))
}

func preflightOf(cl cloud.Provider, dns *cloudflare.Client) preflight.Service {
	d := preflight.Deps{Cloud: cl, DNS: dns}
	if git.Available() {
		d.GitLsRemote = git.LsRemote
	}
	return preflight.Service{Deps: d}
}

// Up is the use case Up for the machine of c.
func Up(c *model.Machine, o driving.UpOptions) start.Service {
	sleep := o.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	cl, dns := providerOf(c), dnsOf(c) // the run and its preflight share them
	d := start.Deps{
		Preflight: func(ctx context.Context, repoArg string, out, errOut io.Writer) int {
			return preflightOf(cl, dns).Preflight(ctx, c, repoArg, out, errOut)
		},
		Cloud:  cl,
		DNS:    dns,
		Run:    system.Run,
		Stream: system.Stream,
		Keygen: system.Keygen,
		SSH:    sshconfig.Files{},
		Sleep:  sleep,
		Now:    time.Now,
	}
	if !o.NoCode {
		d.OpenVSCode = func(alias, workdir string) { _ = vscode().Launch(alias, workdir) }
	}
	return start.Service{Deps: d}
}

// Down is the use case Down for the machine of c.
func Down(c *model.Machine, o driving.DownOptions) stop.Service {
	return stop.Service{Deps: stop.Deps{
		Cloud: providerOf(c),
		DNS:   dnsOf(c),
		Run:   system.Run,
		Ask:   o.Ask,
		SSH:   sshconfig.Files{},
	}}
}

// Check is the use case Check for the machine of c.
func Check(c *model.Machine) check.Service {
	return check.Service{Deps: check.Deps{
		Cloud:      providerOf(c),
		LookupIPv4: resolver.LookupIPv4,
		Nameserver: resolver.Nameserver(),
		Run:        system.Run,
		SSH:        sshconfig.Files{},
	}}
}

// vscode starts VS Code without the credentials of any provider in its environment.
func vscode() editor.VSCode {
	var secrets []string
	for _, name := range model.ProviderNames() {
		info, _ := model.LookupProvider(name)
		secrets = append(secrets, info.Secrets...)
	}
	return editor.VSCode{Secrets: secrets}
}
