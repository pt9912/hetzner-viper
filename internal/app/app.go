// Package app is the composition root: it makes the adapters, plugs them into the use cases and
// gives the use cases to the command line. Nothing else knows which adapter is behind a port.
package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/aws"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/azure"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/cloudflare"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/ecb"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/editor"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/gcp"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/git"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/hetzner"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/nodns"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/resolver"
	runlogfile "github.com/pt9912/hetzner-viper/internal/adapter/driven/runlog"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/settingsfile"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/sshconfig"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/system"
	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
	"github.com/pt9912/hetzner-viper/internal/port/driven/dns"
	"github.com/pt9912/hetzner-viper/internal/port/driven/runlog"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
	"github.com/pt9912/hetzner-viper/internal/usecase/check"
	"github.com/pt9912/hetzner-viper/internal/usecase/find"
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
			Find:      func(get func(string) string, notice io.Writer) driving.Find { return Find(get, notice) },
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

// DNS is the DNS service chosen for a machine.
type DNS struct{ dns.Provider }

func dnsOf(c *model.Machine) DNS {
	if c.DNSProvider == model.DNSNone {
		return DNS{nodns.Client{}}
	}
	return DNS{&cloudflare.Client{Token: c.Get("CLOUDFLARE_API_TOKEN"), ZoneID: c.Get("CLOUDFLARE_ZONE_ID")}}
}

// Preflight is the use case Preflight for the machine of c.
func Preflight(c *model.Machine) preflight.Service {
	return preflightOf(providerOf(c), dnsOf(c))
}

func preflightOf(cl cloud.Provider, dns DNS) preflight.Service {
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

// Find is the use case Find over the price lists of the providers; a provider whose credentials
// get does not give is named and left out. A provider that prices a region at a time looks in the
// location of SERVER_LOCATION if PROVIDER names it, else in its default region.
func Find(get func(string) string, notice io.Writer) find.Service {
	notice = &lockedWriter{w: notice}
	var sources []find.Source
	for _, name := range model.ProviderNames() {
		info, _ := model.LookupProvider(name)
		src := find.Source{Name: name}
		setCatalog(&src, get, notice)
		src.Location = defaultLocation(name, info, get)
		sources = append(sources, src)
	}
	return find.Service{Deps: find.Deps{Sources: sources, Rates: ecb.New(cacheDir())}}
}

// defaultLocation is where the price list of a provider is read if --location says nothing; ""
// for a provider that lists all its locations at once.
func defaultLocation(name string, info model.ProviderInfo, get func(string) string) string {
	if name == "hetzner" {
		return ""
	}
	if loc := get("SERVER_LOCATION"); loc != "" && strings.ToLower(get("PROVIDER")) == name {
		return loc
	}
	return info.Region
}

// cacheDir is where viper keeps what it can read again, "" if the system has no such place.
func cacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "viper")
}

// setCatalog makes the price list of the provider and what words its errors.
func setCatalog(src *find.Source, get func(string) string, notice io.Writer) {
	switch src.Name {
	case "hetzner":
		src.Catalog, src.Describe = hetzner.NewCatalog(get("HCLOUD_TOKEN"), cacheDir(), notice), hetzner.Error
	case "aws":
		c := aws.NewCatalog(cacheDir(), notice)
		src.Catalog, src.Describe = c, c.Describe
	case "azure":
		c := azure.NewCatalog(get("AZURE_SUBSCRIPTION_ID"), cacheDir(), notice)
		src.Catalog, src.Describe = c, c.Describe
	case "gcp":
		c := gcp.NewCatalog(get("GCP_PROJECT"), cacheDir(), notice)
		src.Catalog, src.Describe = c, c.Describe
	default:
		src.Skip = "no price list"
	}
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

// lockedWriter lets the providers, which are asked at the same time, write their notes without one
// breaking into another's line.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
