package natsgroup_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/natsserver"
	"github.com/pt9912/hetzner-viper/internal/usecase/natsgroup"
)

func key(c string) string { return "U" + strings.Repeat(c, 55) }

// server is a NATS server's configuration file, with the optimistic check of natsconfig.sh.
type server struct {
	conf        string
	version     int
	writes      int
	meanwhile   func() // runs once between the first read and the first write: somebody else writes
	failWriteAs error
}

func (s *server) run(_ context.Context, stdin, _ string, args ...string) (string, string, error) {
	last := args[len(args)-1]
	switch {
	case strings.Contains(last, "sha256 none"): // the read script
		return "sha256 v" + string(rune('0'+s.version)) + "\n" + s.conf, "", nil
	case strings.Contains(last, "changed meanwhile"): // the write script, with the version it expects as last argument
		if s.meanwhile != nil {
			f := s.meanwhile
			s.meanwhile = nil
			f()
		}
		if s.failWriteAs != nil {
			return "nats config: " + s.failWriteAs.Error() + "\n", "", errors.New("exit status 3") // the script says why on stdout
		}
		if !strings.HasSuffix(last, "'v"+string(rune('0'+s.version))+"'") {
			return "nats config: the file changed meanwhile\n", "", errors.New("exit status 3")
		}
		s.conf, s.version = stdin, s.version+1
		s.writes++
		return "nats config: written\n", "", nil
	}
	return "", "", errors.New("unexpected: " + last)
}

func start(t *testing.T, clients ...natsserver.Client) *server {
	t.Helper()
	conf, err := natsserver.Config{Group: "dev", Host: "10.1.0.2", Port: 4222, Clients: clients}.Render()
	if err != nil {
		t.Fatal(err)
	}
	return &server{conf: conf, version: 1}
}

func names(s *server) string {
	var out []string
	for _, c := range natsserver.ParseClients(s.conf) {
		out = append(out, c.Machine)
	}
	return strings.Join(out, ",")
}

func TestUpdateAddsAndRemovesAClient(t *testing.T) {
	s := start(t, natsserver.Client{Machine: "alpha", PublicKey: key("A")})
	o := natsgroup.Ops{Run: s.run}
	tgt := natsgroup.Target{Config: "c", Alias: "a"}
	add := func(l []natsserver.Client) ([]natsserver.Client, error) {
		return natsserver.WithClient(l, natsserver.Client{Machine: "beta", PublicKey: key("B")}), nil
	}
	if err := o.Update(context.Background(), tgt, "dev", natsserver.Endpoint{}, add); err != nil || names(s) != "alpha,beta" {
		t.Fatalf("%v %q", err, names(s))
	}
	drop := func(l []natsserver.Client) ([]natsserver.Client, error) {
		return natsserver.WithoutClient(l, "beta"), nil
	}
	if err := o.Update(context.Background(), tgt, "dev", natsserver.Endpoint{}, drop); err != nil || names(s) != "alpha" {
		t.Fatalf("%v %q", err, names(s))
	}
	// the address of the server is the one in the file
	if e, ok := natsserver.ParseEndpoint(s.conf); !ok || e.Host != "10.1.0.2" {
		t.Errorf("%+v", e)
	}
}

func TestUpdateReadsAgainWhenSomebodyElseWroteInBetween(t *testing.T) {
	s := start(t, natsserver.Client{Machine: "alpha", PublicKey: key("A")})
	// while beta prepares its change, gamma registers
	s.meanwhile = func() {
		conf, _ := natsserver.Config{Group: "dev", Host: "10.1.0.2", Port: 4222, Clients: []natsserver.Client{
			{Machine: "alpha", PublicKey: key("A")}, {Machine: "gamma", PublicKey: key("C")}}}.Render()
		s.conf, s.version = conf, s.version+1
	}
	o := natsgroup.Ops{Run: s.run}
	add := func(l []natsserver.Client) ([]natsserver.Client, error) {
		return natsserver.WithClient(l, natsserver.Client{Machine: "beta", PublicKey: key("B")}), nil
	}
	if err := o.Update(context.Background(), natsgroup.Target{}, "dev", natsserver.Endpoint{}, add); err != nil {
		t.Fatal(err)
	}
	if got := names(s); got != "alpha,beta,gamma" {
		t.Errorf("gamma's entry must survive: %q", got)
	}
}

func TestUpdateWritesNothingWhenThereIsNothingToChange(t *testing.T) {
	s := start(t, natsserver.Client{Machine: "alpha", PublicKey: key("A")})
	err := natsgroup.Ops{Run: s.run}.Update(context.Background(), natsgroup.Target{}, "dev", natsserver.Endpoint{}, func([]natsserver.Client) ([]natsserver.Client, error) {
		return nil, natsgroup.ErrNoChange
	})
	if err != nil || s.writes != 0 {
		t.Errorf("%v, %d writes", err, s.writes)
	}
}

func TestUpdateGivesUpAfterSixLostRaces(t *testing.T) {
	s := start(t, natsserver.Client{Machine: "alpha", PublicKey: key("A")})
	s.failWriteAs = errors.New("the file changed meanwhile")
	err := natsgroup.Ops{Run: s.run}.Update(context.Background(), natsgroup.Target{}, "dev", natsserver.Endpoint{}, func(l []natsserver.Client) ([]natsserver.Client, error) { return l, nil })
	if err == nil || !strings.Contains(err.Error(), "changed meanwhile") {
		t.Errorf("%v", err)
	}
}

func TestUpdateStartsAServerWithoutAConfiguration(t *testing.T) {
	s := &server{version: 0}
	o := natsgroup.Ops{Run: func(ctx context.Context, stdin, name string, args ...string) (string, string, error) {
		if strings.Contains(args[len(args)-1], "sha256 none") {
			return "sha256 none\n", "", nil // no file yet
		}
		s.conf, s.version = stdin, 1
		return "nats config: written\n", "", nil
	}}
	add := func(l []natsserver.Client) ([]natsserver.Client, error) {
		return natsserver.WithClient(l, natsserver.Client{Machine: "alpha", PublicKey: key("A")}), nil
	}
	if err := o.Update(context.Background(), natsgroup.Target{}, "dev", natsserver.Endpoint{Host: "10.9.0.2", Port: 4222}, add); err != nil {
		t.Fatal(err)
	}
	if e, ok := natsserver.ParseEndpoint(s.conf); !ok || e.Host != "10.9.0.2" || names(s) != "alpha" {
		t.Errorf("%+v %q", e, names(s))
	}
}

func TestAScriptIsRunAgainAfterALostConnectionButNotAfterAFailedScript(t *testing.T) {
	calls, sleeps := 0, 0
	lost := func(context.Context, string, string, ...string) (string, string, error) {
		calls++
		if calls < 3 {
			return "", "kex_exchange_identification: read: Connection reset by peer\n", errors.New("exit status 255")
		}
		return "nats ok\n", "", nil
	}
	o := natsgroup.Ops{Run: lost, Sleep: func(time.Duration) { sleeps++ }}
	out, err := o.On(context.Background(), natsgroup.Target{}, true, "", "echo", "x")
	if err != nil || out != "nats ok\n" || calls != 3 || sleeps != 2 {
		t.Fatalf("%q %v calls %d sleeps %d", out, err, calls, sleeps)
	}
	calls = 0
	failed := func(context.Context, string, string, ...string) (string, string, error) {
		calls++
		return "nats cert: openssl failed\n", "", errors.New("exit status 1")
	}
	if _, err := (natsgroup.Ops{Run: failed, Sleep: func(time.Duration) {}}).On(context.Background(), natsgroup.Target{}, true, "", "echo"); err == nil || calls != 1 {
		t.Errorf("a script that failed is not run again: %v calls %d", err, calls)
	}
	calls = 0
	never := func(context.Context, string, string, ...string) (string, string, error) {
		calls++
		return "", "Connection refused\n", errors.New("exit status 255")
	}
	if _, err := (natsgroup.Ops{Run: never, Sleep: func(time.Duration) {}}).On(context.Background(), natsgroup.Target{}, true, "", "echo"); err == nil || calls != 12 {
		t.Errorf("gives up after 12 tries: %v calls %d", err, calls)
	}
}
