package natsserver_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/natsserver"
)

func key(t *testing.T) string {
	t.Helper()
	id, err := natsserver.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return id.PublicKey
}

func TestTheOperatorGetsOnlyItsOwnSubtreeAndReadsTheGroup(t *testing.T) {
	pub, sub := natsserver.OperatorPermissions("dev", "operator", false)
	if len(pub) != 1 || pub[0] != "viper.dev.operator.>" || len(sub) != 1 || sub[0] != "viper.dev.>" {
		t.Errorf("%v %v", pub, sub)
	}
	mpub, _ := natsserver.Permissions("dev", "alpha", false)
	for _, p := range mpub {
		if strings.Contains(p, ".req.") {
			for _, o := range pub {
				if o == p {
					t.Errorf("the operator has the request subjects of a machine: %s", p)
				}
			}
		}
	}
}

func TestTheOperatorIsRenderedWithItsRightsAndReadBack(t *testing.T) {
	clients := []natsserver.Client{
		{Machine: "alpha", PublicKey: key(t)},
		{Machine: "operator", PublicKey: key(t), Operator: true},
	}
	conf, err := natsserver.Config{Group: "dev", Host: "10.0.0.5", Port: 4222, Clients: clients}.Render()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(conf, "# viper-operator operator ") || !strings.Contains(conf, "# viper-client alpha ") {
		t.Errorf("registry lines:\n%s", conf)
	}
	i := strings.Index(conf, "# operator\n")
	if i < 0 {
		t.Fatalf("no user block for the operator:\n%s", conf)
	}
	block := conf[i:]
	if j := strings.Index(block[1:], "# alpha"); j >= 0 {
		block = block[:j+1]
	}
	if !strings.Contains(block, `"viper.dev.operator.>"`) || strings.Contains(block, ".req.") || strings.Contains(block, "_INBOX") {
		t.Errorf("rights of the operator:\n%s", block)
	}
	back := natsserver.ParseClients(conf)
	if len(back) != 2 || !back[1].Operator || back[1].Machine != "operator" || back[0].Operator {
		t.Errorf("%+v", back)
	}
}

func TestAMachineAndTheOperatorCannotShareAName(t *testing.T) {
	list := []natsserver.Client{{Machine: "alpha", PublicKey: key(t)}, {Machine: "operator", PublicKey: key(t), Operator: true}}
	// a machine of the operator's name does not take the operator's key
	if _, err := natsserver.WithEntry(list, natsserver.Client{Machine: "operator", PublicKey: key(t)}); !errors.Is(err, natsserver.ErrNameTaken) {
		t.Errorf("%v", err)
	}
	// the operator does not take the key of a machine
	if _, err := natsserver.WithEntry(list, natsserver.Client{Machine: "alpha", PublicKey: key(t), Operator: true}); !errors.Is(err, natsserver.ErrNameTaken) {
		t.Errorf("%v", err)
	}
	// the same kind replaces its key
	got, err := natsserver.WithEntry(list, natsserver.Client{Machine: "operator", PublicKey: key(t), Operator: true})
	if err != nil || len(got) != 2 {
		t.Errorf("%+v %v", got, err)
	}
	// a configuration with both is refused when it is rendered
	both := append(list, natsserver.Client{Machine: "operator", PublicKey: key(t)})
	if _, err := (natsserver.Config{Group: "dev", Host: "10.0.0.5", Port: 4222, Clients: both}).Render(); err == nil {
		t.Error("the same name twice")
	}
}

func TestRemovingAMachineLeavesTheOperatorAndTheOtherWayAround(t *testing.T) {
	list := []natsserver.Client{{Machine: "alpha", PublicKey: "a"}, {Machine: "operator", PublicKey: "o", Operator: true}}
	if got := natsserver.WithoutClient(list, "operator"); len(got) != 2 {
		t.Errorf("down of a machine named like the operator removed the operator: %+v", got)
	}
	if got := natsserver.WithoutClient(list, "alpha"); len(got) != 1 || !got[0].Operator {
		t.Errorf("%+v", got)
	}
	if got := natsserver.WithoutOperator(list, "alpha"); len(got) != 2 {
		t.Errorf("revoke removed a machine: %+v", got)
	}
	if got := natsserver.WithoutOperator(list, "operator"); len(got) != 1 || got[0].Operator {
		t.Errorf("%+v", got)
	}
}
