package natsop_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/natsserver"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
	"github.com/pt9912/hetzner-viper/internal/usecase/natsgroup"
	"github.com/pt9912/hetzner-viper/internal/usecase/natsop"
)

func key(c string) string { return "U" + strings.Repeat(c, 55) }

const cert = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"

// host is a machine: the server (with a configuration) or another one (without).
type host struct {
	conf    string // "" for a machine that is not the server
	version int
	err     error
}

func (h *host) run(_ context.Context, stdin, _ string, args ...string) (string, string, error) {
	last := args[len(args)-1]
	switch {
	case h.err != nil:
		return "", "Connection refused", h.err
	case strings.Contains(last, "sha256 none"): // the read script
		if h.conf == "" {
			return "sha256 none\n", "", nil
		}
		return "sha256 v" + string(rune('0'+h.version)) + "\n" + h.conf, "", nil
	case strings.Contains(last, "changed meanwhile"): // the write script
		h.conf, h.version = stdin, h.version+1
		return "nats config: written\n", "", nil
	case strings.Contains(last, "server.crt"):
		return cert, "", nil
	}
	return "", "", errors.New("unexpected: " + last)
}

func serverHost(t *testing.T, clients ...natsserver.Client) *host {
	t.Helper()
	conf, err := natsserver.Config{Group: "dev", Host: "10.1.0.2", Port: 4222, Clients: clients}.Render()
	if err != nil {
		t.Fatal(err)
	}
	return &host{conf: conf, version: 1}
}

// service runs every machine against its own fake; the target's alias names the machine.
func service(hosts map[string]*host) natsop.Service {
	return natsop.Service{Ops: natsgroup.Ops{Sleep: func(time.Duration) {}, Run: func(ctx context.Context, stdin, name string, args ...string) (string, string, error) {
		for _, a := range args {
			if h, ok := hosts[a]; ok {
				return h.run(ctx, stdin, name, args...)
			}
		}
		return "", "", errors.New("no such machine")
	}}}
}

func machines(names ...string) []driving.NATSMachine {
	var out []driving.NATSMachine
	for _, n := range names {
		out = append(out, driving.NATSMachine{Name: n, Target: driving.NATSTarget{Config: "c", Alias: n}})
	}
	return out
}

func TestFindTakesTheMachineThatHoldsTheRegistry(t *testing.T) {
	hosts := map[string]*host{"alpha": {}, "beta": serverHost(t, natsserver.Client{Machine: "beta", PublicKey: key("B")}), "gamma": {}}
	info, err := service(hosts).Find(context.Background(), machines("alpha", "beta", "gamma"))
	if err != nil || info.Machine.Name != "beta" || info.Host != "10.1.0.2" || info.Port != 4222 || info.CA != cert {
		t.Fatalf("%+v %v", info, err)
	}
	if _, err := service(map[string]*host{"alpha": {}, "gamma": {}}).Find(context.Background(), machines("alpha", "gamma")); err == nil {
		t.Error("none of them is the server")
	}
	hosts["alpha"].err = errors.New("exit status 255")
	if info, err := service(hosts).Find(context.Background(), machines("alpha", "beta")); err != nil || info.Machine.Name != "beta" {
		t.Errorf("a machine that cannot be reached is skipped: %+v %v", info, err)
	}
}

func TestRegisterAndRevokeKeepTheMachines(t *testing.T) {
	srv := serverHost(t, natsserver.Client{Machine: "alpha", PublicKey: key("A")}, natsserver.Client{Machine: "beta", PublicKey: key("B")})
	s := service(map[string]*host{"alpha": srv})
	ctx := context.Background()
	info, err := s.Find(ctx, machines("alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Register(ctx, "dev", info, "operator", key("O")); err != nil {
		t.Fatal(err)
	}
	got := natsserver.ParseClients(srv.conf)
	if len(got) != 3 || !got[2].Operator || got[2].Machine != "operator" || got[2].PublicKey != key("O") {
		t.Fatalf("%+v", got)
	}
	// the same name again replaces the key
	if err := s.Register(ctx, "dev", info, "operator", key("P")); err != nil {
		t.Fatal(err)
	}
	if got := natsserver.ParseClients(srv.conf); len(got) != 3 || got[2].PublicKey != key("P") {
		t.Errorf("%+v", got)
	}
	info, _ = s.Find(ctx, machines("alpha"))
	if len(info.Operators) != 1 || info.Operators[0] != "operator" {
		t.Errorf("%+v", info.Operators)
	}
	if err := s.Revoke(ctx, "dev", info, "operator"); err != nil {
		t.Fatal(err)
	}
	if got := natsserver.ParseClients(srv.conf); len(got) != 2 || got[0].Operator || got[1].Operator {
		t.Errorf("%+v", got)
	}
	writes := srv.version
	if err := s.Revoke(ctx, "dev", info, "operator"); err != nil || srv.version != writes {
		t.Errorf("an operator that is not there changes nothing: %v", err)
	}
}

func TestTheOperatorCannotTakeTheNameOfAMachine(t *testing.T) {
	srv := serverHost(t, natsserver.Client{Machine: "alpha", PublicKey: key("A")})
	s := service(map[string]*host{"alpha": srv})
	info, _ := s.Find(context.Background(), machines("alpha"))
	err := s.Register(context.Background(), "dev", info, "alpha", key("O"))
	if !errors.Is(err, natsserver.ErrNameTaken) {
		t.Fatalf("%v", err)
	}
	if got := natsserver.ParseClients(srv.conf); len(got) != 1 || got[0].Operator || got[0].PublicKey != key("A") {
		t.Errorf("the key of the machine was replaced: %+v", got)
	}
}
