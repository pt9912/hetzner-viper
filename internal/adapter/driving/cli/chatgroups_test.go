package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/settingsfile"
	"github.com/pt9912/hetzner-viper/internal/adapter/driving/chatweb"
	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

// A machine that two groups name runs in one of them: the other group does not have it, and a message to it goes nowhere in that group.
func TestAMachineOfTwoGroupsBelongsToTheGroupItRunsInAndNotToTheOther(t *testing.T) {
	dir, env := natsGroup(t, "")
	if err := os.WriteFile(filepath.Join(dir, "groups", "ops.env"), []byte("GROUP_MACHINES=beta,delta\nNATS_ENABLED=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := env
	e.Group = "dev"
	c, err := settingsfile.Load(dir, "beta", e, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(c.GroupState), 0o700); err != nil { // beta was started in dev
		t.Fatal(err)
	}
	if err := os.WriteFile(c.GroupState, []byte("dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, h := fakeTools(t)
	h.Env = func() (settings.Env, error) { return env, nil }
	b := cli.ChatBackend(&cli.Opts{Host: h, Dir: dir}, env)
	gs, err := b.Groups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	names := map[string][]string{}
	for _, g := range gs {
		for _, m := range g.Machines {
			names[g.Name] = append(names[g.Name], m.Name)
		}
	}
	t.Logf("groups and machines: %v", names)
	has := func(g, m string) bool {
		for _, x := range names[g] {
			if x == m {
				return true
			}
		}
		return false
	}
	if !has("dev", "beta") || has("ops", "beta") {
		t.Errorf("beta runs in dev: %v", names)
	}
	_, err = b.Send(context.Background(), "ops", chatweb.Outgoing{Kind: "msg", To: "beta", Text: "x"}, nil)
	if err == nil || strings.Contains(err.Error(), "tunnel") {
		t.Errorf("a message to beta in ops: %v", err)
	}
	t.Logf("send to beta in ops: %v", err)
}
