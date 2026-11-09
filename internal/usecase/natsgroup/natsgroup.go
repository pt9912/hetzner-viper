// Package natsgroup is what viper up and viper down do together on the machines of a NATS group:
// reach another machine of the group by ssh, and change the list of the machines that may use the
// server without losing the changes of others.
package natsgroup

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"path/filepath"
	"strings"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/natsserver"
	"github.com/pt9912/hetzner-viper/internal/port/driven/sshconfig"
	"github.com/pt9912/hetzner-viper/internal/port/driven/system"
)

var (
	//go:embed scripts/natsread.sh
	readScript string
	//go:embed scripts/natsconfig.sh
	configScript string
)

// ErrNoChange is returned by the change function of Update when there is nothing to write.
var ErrNoChange = errors.New("nothing to change")

// Target is a machine that viper reaches by ssh: the file with its ssh config and its alias in it.
type Target struct{ Config, Alias string }

// Ops are the operations over ssh. Output lines that start with "nats" go to Out unless quiet.
type Ops struct {
	Run system.Runner
	SSH sshconfig.Store
	Out io.Writer
	// Sleep waits between the tries after a lost connection; nil: time.Sleep.
	Sleep system.Sleeper
	// Jitter returns a random time up to limit; Update waits that long after it lost a race with
	// another machine, so that machines that start together do not collide again at the same moment.
	// nil: no extra wait.
	Jitter func(limit time.Duration) time.Duration
}

// connectionTries is how often a script is run again after ssh itself failed (a server that is
// still starting, or restarts once after its updates). The scripts can be run again: each of them
// leaves the machine as it would after one run.
const connectionTries = 12

// lostConnection tells whether the stderr of ssh says that the connection failed, as opposed to
// the script failing.
func lostConnection(stderr string) bool {
	for _, s := range []string{"Connection reset", "Connection closed", "Connection refused", "Connection timed out", "kex_exchange_identification", "Broken pipe", "No route to host"} {
		if strings.Contains(stderr, s) {
			return true
		}
	}
	return false
}

// Self is the machine c itself.
func Self(c *model.Machine) Target { return Target{c.SSHConfig, c.SSHAlias} }

// Peer is another machine of the group (the server, seen from a client). Its ssh config is written
// here if this computer does not have it; without a pin for it, the host CA vouches for it.
func (o Ops) Peer(c *model.Machine, s model.Server) (Target, error) {
	cc := *c
	cc.ServerName = s.Name
	cc.Name = strings.TrimPrefix(s.Name, c.ServerPrefix+"-")
	cc.SSHAlias = "hetzner-" + s.Name
	cc.DNSHost = s.Name + "." + c.DNSDomain
	cc.KnownPin = filepath.Join(c.SSHDir, "known_hosts_hetzner_"+s.Name)
	cc.SSHConfig = filepath.Join(c.SSHDir, "config_hetzner_"+s.Name)
	if s.IPv4 == "" {
		return Target{}, fmt.Errorf("the server %s has no public address", s.Name)
	}
	if err := o.SSH.WriteConfig(&cc, s.IPv4); err != nil {
		return Target{}, err
	}
	return Target{cc.SSHConfig, cc.SSHAlias}, nil
}

// On runs a script on the machine with data on stdin and returns what it printed. The script is
// the argument of bash -c, because stdin carries the data.
func (o Ops) On(ctx context.Context, t Target, quiet bool, stdin, script string, args ...string) (string, error) {
	cmd := "bash -c " + model.Quote(script) + " nats " + model.QuoteAll(args...)
	sshArgs := []string{"-F", t.Config, "-o", "BatchMode=yes", t.Alias, cmd}
	if stdin == "" {
		sshArgs = append([]string{"-n"}, sshArgs...)
	}
	stdout, stderr, err := o.Run(ctx, stdin, "ssh", sshArgs...)
	for try := 1; err != nil && lostConnection(stderr) && try < connectionTries && ctx.Err() == nil; try++ {
		if o.Sleep != nil {
			o.Sleep(5 * time.Second)
		} else {
			time.Sleep(5 * time.Second)
		}
		stdout, stderr, err = o.Run(ctx, stdin, "ssh", sshArgs...)
	}
	if !quiet && o.Out != nil {
		for _, l := range strings.Split(strings.TrimRight(stdout, "\n"), "\n") {
			if strings.HasPrefix(l, "nats") {
				fmt.Fprintln(o.Out, l)
			}
		}
	}
	if err != nil {
		return stdout, fmt.Errorf("%s", strings.TrimSpace(lastLines(stdout, 6)+"\n"+stderr))
	}
	return stdout, nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// Clients reads the machines that are registered at the server.
func (o Ops) Clients(ctx context.Context, server Target) ([]natsserver.Client, error) {
	out, err := o.On(ctx, server, true, "", readScript, natsserver.Dir)
	if err != nil {
		return nil, err
	}
	_, conf, _ := strings.Cut(out, "\n")
	return natsserver.ParseClients(conf), nil
}

// Update changes the list of the machines at the server and has the server read it. Several
// machines may do this at once: the file is read together with its hash, and written back only if
// nobody changed it in between; otherwise it is read again. first is where a server listens that
// has no configuration yet. change gets the list and returns the new one; if it returns an error,
// nothing is written.
func (o Ops) Update(ctx context.Context, server Target, group string, first natsserver.Endpoint, change func([]natsserver.Client) ([]natsserver.Client, error)) error {
	var lastErr error
	for attempt := 0; attempt < updateTries; attempt++ {
		out, err := o.On(ctx, server, true, "", readScript, natsserver.Dir)
		if err != nil {
			return err
		}
		head, conf, _ := strings.Cut(out, "\n")
		sha := strings.TrimPrefix(head, "sha256 ")
		end, ok := natsserver.ParseEndpoint(conf)
		if !ok {
			end = first
		}
		clients, err := change(natsserver.ParseClients(conf))
		if errors.Is(err, ErrNoChange) {
			return nil
		}
		if err != nil {
			return err
		}
		cfg, err := natsserver.Config{Group: group, Host: end.Host, Port: end.Port, Clients: clients}.Render()
		if err != nil {
			return err
		}
		_, err = o.On(ctx, server, false, cfg, configScript, natsserver.Dir, strings.TrimSuffix(natsserver.Unit, ".service"), sha)
		if err == nil {
			return nil
		}
		if !strings.Contains(err.Error(), "changed meanwhile") {
			return err
		}
		lastErr = err
		if o.Jitter != nil {
			pause(o.Jitter(time.Duration(attempt+1) * 500 * time.Millisecond))
		}
	}
	return lastErr
}

// updateTries is how often Update reads and writes again after another machine changed the file
// in between. Eight machines that join at once need about one try each.
const updateTries = 12

// pause waits for real: it spreads out machines that lost a race, which Sleep (a wait after a lost
// connection that some callers switch off) must not switch off.
func pause(d time.Duration) {
	if d > 0 {
		time.Sleep(d)
	}
}

// RandomJitter is the Jitter of the real runs.
func RandomJitter(limit time.Duration) time.Duration {
	if limit <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(limit))) //nolint:gosec // only spreads out retries
}
