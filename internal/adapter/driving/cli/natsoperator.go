package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pt9912/hetzner-viper/internal/natsmcp"
	"github.com/pt9912/hetzner-viper/internal/natsserver"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

// The operator of a NATS group (docs/plan/nats-cli.md, stage 2): viper nats init registers a key of its own
// at the server, and viper nats then speaks under the name of the operator, from this computer, through an
// ssh tunnel to the server. The key stays here; nothing of it goes to a machine.

// natsGroup is the machines of a NATS group as viper nats sees them.
type natsGroup struct {
	name     string
	order    []string
	ms       map[string]natsMachine
	operator string // the name the operator sends under
}

func loadNatsGroup(o *options, env settings.Env, group string) (*natsGroup, error) {
	o.group = group
	g, err := groupMachines(o, env)
	if err != nil {
		return nil, err
	}
	ng := &natsGroup{name: group, order: g.Machines, ms: map[string]natsMachine{}}
	for _, name := range g.Machines {
		c, err := o.host().Services.Settings.Load(settingsDir(o), name, env, false)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		ng.ms[name] = natsMachine{name, c}
		if c.NATS.Enabled && ng.operator == "" {
			ng.operator = c.NATS.Operator
		}
	}
	if ng.operator == "" {
		return nil, fmt.Errorf("no machine of the group %s has NATS_ENABLED=true", group)
	}
	for name := range ng.ms {
		if name == ng.operator {
			return nil, fmt.Errorf("the machine %s has the name of the operator (NATS_OPERATOR_NAME): rename one of them", name)
		}
	}
	return ng, nil
}

// keyFile is where the seed of the operator of the group is.
func (g *natsGroup) keyFile() string {
	return filepath.Join(g.ms[g.order[0]].cfg.StateDir, "nats", g.name, "operator.nk")
}

// targets are the machines that can be reached (they have run viper up).
func (g *natsGroup) targets() []driving.NATSMachine {
	var out []driving.NATSMachine
	for _, name := range g.order {
		if m := g.ms[name]; exists(m.cfg.SSHConfig) {
			out = append(out, driving.NATSMachine{Name: name, Target: driving.NATSTarget{Config: m.cfg.SSHConfig, Alias: m.cfg.SSHAlias}})
		}
	}
	return out
}

// find returns the server of the group.
func (g *natsGroup) find(ctx context.Context, o *options) (driving.NATSServerInfo, error) {
	if o.host().Services.NATSOp == nil {
		return driving.NATSServerInfo{}, errors.New("viper nats cannot reach the server here")
	}
	ts := g.targets()
	if len(ts) == 0 {
		return driving.NATSServerInfo{}, errors.New("no machine of the group has an SSH config: run viper up first")
	}
	return o.host().Services.NATSOp.Find(ctx, ts)
}

// runNatsInit makes the key of the operator, or keeps the one that is there, and registers it at the server.
func runNatsInit(ctx context.Context, o *options, env settings.Env, group string, stdout io.Writer) error {
	g, err := loadNatsGroup(o, env, group)
	if err != nil {
		return err
	}
	ctx, stop := notifyContext(ctx)
	defer stop()
	srv, err := g.find(ctx, o)
	if err != nil {
		return err
	}
	seed, err := os.ReadFile(g.keyFile())
	switch {
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		id, ierr := natsserver.NewIdentity()
		if ierr != nil {
			return ierr
		}
		if seed, err = []byte(id.Seed+"\n"), writePrivate(g.keyFile(), id.Seed+"\n"); err != nil {
			return err
		}
	default:
		return err
	}
	pub, err := natsserver.PublicKeyOf(string(seed))
	if err != nil {
		return fmt.Errorf("%s: %w", g.keyFile(), err)
	}
	if err := o.host().Services.NATSOp.Register(ctx, group, srv, g.operator, pub); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "The operator %s is registered at the NATS server %s (group %s).\n", g.operator, srv.Machine.Name, group)
	fmt.Fprintf(stdout, "Its key: %s. viper nats now sends as %s unless --from names a machine.\n", g.keyFile(), g.operator)
	fmt.Fprintf(stdout, "The agents see %s as the sender; it can read everything in the group. Remove it with viper nats revoke %s.\n", g.operator, group)
	return nil
}

// runNatsRevoke removes the operator from the server and the key from this computer.
func runNatsRevoke(ctx context.Context, o *options, env settings.Env, group string, stdout io.Writer) error {
	g, err := loadNatsGroup(o, env, group)
	if err != nil {
		return err
	}
	ctx, stop := notifyContext(ctx)
	defer stop()
	if srv, err := g.find(ctx, o); err != nil {
		if _, serr := os.Stat(g.keyFile()); serr != nil {
			return err
		}
		return fmt.Errorf("the key was kept because the server could not be reached (%w): run viper nats revoke again when it is up", err)
	} else if err := o.host().Services.NATSOp.Revoke(ctx, group, srv, g.operator); err != nil {
		return err
	}
	if err := os.Remove(g.keyFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fmt.Fprintf(stdout, "The operator %s is removed from the group %s and its key is deleted.\n", g.operator, group)
	return nil
}

// writePrivate writes a file that only the user can read, in a directory that only the user can enter.
func writePrivate(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o600)
}

// operatorSession is what viper nats needs to speak as the operator: the tunnel, the certificate and the
// key, behind a natscli remote that runs the subcommands in this process.
type operatorSession struct {
	remote driving.NATSRemote
	close  func()
}

func openOperator(ctx context.Context, o *options, g *natsGroup) (*operatorSession, error) {
	srv, err := g.find(ctx, o)
	if err != nil {
		return nil, err
	}
	open := o.host().OpenTunnel
	if open == nil {
		open = func(ctx context.Context, via driving.NATSTarget, host string, port int) (string, func(), error) {
			return sshTunnel(ctx, o, via, host, port)
		}
	}
	addr, stopTunnel, err := open(ctx, srv.Machine.Target, srv.Host, srv.Port)
	if err != nil {
		return nil, fmt.Errorf("the ssh tunnel to the NATS server: %w", err)
	}
	cfg := natsmcp.Config{URL: "tls://" + addr, SeedFile: g.keyFile(), Names: natsmcp.Names{Group: g.name, Me: g.operator},
		MaxText: natsmcp.DefaultMaxText, TLSServerName: srv.Host}
	dir, err := os.MkdirTemp("", "viper-nats-")
	if err != nil {
		stopTunnel()
		return nil, err
	}
	stop := func() { stopTunnel(); _ = os.RemoveAll(dir) }
	if srv.CA != "" { // the certificate is read fresh each time: it expires, and a new group has a new one
		cfg.CAFile = filepath.Join(dir, "ca.pem")
		if err := os.WriteFile(cfg.CAFile, []byte(srv.CA), 0o600); err != nil {
			stop()
			return nil, err
		}
	} else {
		cfg.URL, cfg.TLSServerName = "nats://"+addr, ""
	}
	return &operatorSession{close: stop, remote: func(ctx context.Context, _, stdin string, stdout, stderr io.Writer, words ...string) error {
		if len(words) < 2 {
			return errors.New("no subcommand")
		}
		if code := natsmcp.RunCLI(ctx, cfg, words[1], words[2:], strings.NewReader(stdin), stdout, stderr); code != 0 {
			return exitStatus(code)
		}
		return nil
	}}, nil
}

// syncBuffer collects what the tunnel says on stderr, from another goroutine.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(s.b.String())
}

// sshTunnel forwards a free port of this computer to host:port as seen from the machine.
func sshTunnel(ctx context.Context, o *options, via driving.NATSTarget, host string, port int) (string, func(), error) {
	st := o.host().StreamTool
	if st == nil {
		return "", nil, errors.New("viper nats cannot run programs here")
	}
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	local := l.Addr().String()
	_ = l.Close() // ssh binds it next; the gap is short and a clash makes ssh fail (ExitOnForwardFailure)
	tctx, cancel := context.WithCancel(ctx)
	var errs syncBuffer
	done := make(chan error, 1)
	go func() {
		done <- st(tctx, "", io.Discard, &errs, "ssh", "-F", via.Config, "-o", "BatchMode=yes", "-o", "ExitOnForwardFailure=yes",
			"-N", "-L", fmt.Sprintf("%s:%s:%d", local, host, port), via.Alias)
		close(done)
	}()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if c, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", local); err == nil {
			_ = c.Close()
			return local, func() {
				cancel()
				for range done { // until ssh has ended
				}
			}, nil
		}
		select {
		case err := <-done:
			cancel()
			return "", nil, fmt.Errorf("ssh ended: %v %s", err, errs.String())
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			cancel()
			for range done {
			}
			return "", nil, fmt.Errorf("the tunnel did not come up: %s", errs.String())
		}
	}
}
