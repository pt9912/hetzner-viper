package natsmcp

import (
	"fmt"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
)

// Names are the group and the machine this server speaks for.
type Names struct{ Group, Me string }

// ParsePrefix reads VIPER_NATS_SUBJECT_PREFIX ("viper.<group>.<machine>", exactly three tokens).
func ParsePrefix(prefix string) (Names, error) {
	t := strings.Split(prefix, ".")
	if len(t) != 3 || t[0] != "viper" {
		return Names{}, fmt.Errorf("VIPER_NATS_SUBJECT_PREFIX must be viper.<group>.<machine>, not %q", prefix)
	}
	if err := model.CheckNATSName("group", t[1]); err != nil {
		return Names{}, err
	}
	if err := model.CheckNATSName("machine", t[2]); err != nil {
		return Names{}, err
	}
	return Names{Group: t[1], Me: t[2]}, nil
}

// ValidName tells whether s may be a machine or topic name (the alphabet of the plan, which
// keeps names out of subject syntax: no dots, wildcards or blanks).
func ValidName(s string) bool { return model.CheckNATSName("name", s) == nil }

// The subjects the server uses. All of them lie below viper.<group>.<sender>, the one tree that
// the NATS server lets only <sender> publish to, so the third token proves the sender.

// Out is where this machine sends a message, request or answer to a machine.
func (n Names) Out(to string) string { return fmt.Sprintf("viper.%s.%s.out.%s", n.Group, n.Me, to) }

// Pub is where this machine publishes to a topic.
func (n Names) Pub(topic string) string {
	return fmt.Sprintf("viper.%s.%s.pub.%s", n.Group, n.Me, topic)
}

// Presence is where this machine says that it is there.
func (n Names) Presence() string { return fmt.Sprintf("viper.%s.%s.presence", n.Group, n.Me) }

// InSub is what this machine reads for messages addressed to it.
func (n Names) InSub() string { return fmt.Sprintf("viper.%s.*.out.%s", n.Group, n.Me) }

// PubSub is what this machine reads for a topic.
func (n Names) PubSub(topic string) string { return fmt.Sprintf("viper.%s.*.pub.%s", n.Group, topic) }

// PresenceSub is what this machine reads for the presence of the others.
func (n Names) PresenceSub() string { return fmt.Sprintf("viper.%s.*.presence", n.Group) }

// Inbound is a received subject taken apart.
type Inbound struct {
	Kind string // "out", "pub" or "presence"
	From string // the sender, from the subject
	Arg  string // the topic of a "pub"
}

// Parse takes a subject apart. It accepts exactly the shapes of the subscriptions and nothing
// else: the number of tokens and the fixed fourth token must match, and the sender must be a
// valid name other than this machine. The subtree viper.<group>.<x>.req.> is open to every
// machine and says nothing about the sender, so it is never accepted.
func (n Names) Parse(subject string) (Inbound, bool) {
	t := strings.Split(subject, ".")
	if len(t) < 4 || t[0] != "viper" || t[1] != n.Group || !ValidName(t[2]) || t[2] == n.Me {
		return Inbound{}, false
	}
	switch {
	case len(t) == 5 && t[3] == "out" && t[4] == n.Me:
		return Inbound{Kind: "out", From: t[2]}, true
	case len(t) == 5 && t[3] == "pub" && ValidName(t[4]):
		return Inbound{Kind: "pub", From: t[2], Arg: t[4]}, true
	case len(t) == 4 && t[3] == "presence":
		return Inbound{Kind: "presence", From: t[2]}, true
	}
	return Inbound{}, false
}
