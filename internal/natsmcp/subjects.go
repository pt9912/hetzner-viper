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

// Topics is where this machine says which topics it subscribed to (below its own tree, so the third token proves the sender).
// It is a subject of its own, not a field of the presence message: a server of an older version reads the presence strictly and
// would refuse a presence with a field it does not know, so the machine would count as absent for it. It never subscribes to
// this subject and does not see the announcement.
func (n Names) Topics() string { return fmt.Sprintf("viper.%s.%s.topics", n.Group, n.Me) }

// TopicsSub is what this machine reads for the topics of the others.
func (n Names) TopicsSub() string { return fmt.Sprintf("viper.%s.*.topics", n.Group) }

// Caps is where this machine announces its version and its capabilities (docs/plan/nats-objekte.md §5). An older
// viper-nats-mcp does not subscribe to this subject and does not see the announcement.
func (n Names) Caps() string { return fmt.Sprintf("viper.%s.%s.caps", n.Group, n.Me) }

// CapsSub is what this machine reads for the capabilities of the others.
func (n Names) CapsSub() string { return fmt.Sprintf("viper.%s.*.caps", n.Group) }

// InSub is what this machine reads for messages addressed to it.
func (n Names) InSub() string { return fmt.Sprintf("viper.%s.*.out.%s", n.Group, n.Me) }

// AllOutSub is what listen --all reads in addition: the messages between all machines.
func (n Names) AllOutSub() string { return fmt.Sprintf("viper.%s.*.out.*", n.Group) }

// PubSub is what this machine reads for a topic.
func (n Names) PubSub(topic string) string { return fmt.Sprintf("viper.%s.*.pub.%s", n.Group, topic) }

// PresenceSub is what this machine reads for the presence of the others.
func (n Names) PresenceSub() string { return fmt.Sprintf("viper.%s.*.presence", n.Group) }

// Inbound is a received subject taken apart.
type Inbound struct {
	Kind string // "out", "pub", "presence" or "topics"
	From string // the sender, from the subject
	Arg  string // the topic of a "pub"
	To   string // the machine an "out" is for
}

// ParseDirect takes apart a message between two other machines (listen --all). The messages for this machine
// are not accepted: the subscription for them delivers those.
func (n Names) ParseDirect(subject string) (Inbound, bool) {
	t := strings.Split(subject, ".")
	if len(t) != 5 || t[0] != "viper" || t[1] != n.Group || t[3] != "out" || !ValidName(t[2]) || !ValidName(t[4]) || t[2] == n.Me || t[4] == n.Me {
		return Inbound{}, false
	}
	return Inbound{Kind: "out", From: t[2], To: t[4]}, true
}

// selfAnnouncement says whether the fourth token names a subject a machine announces itself on.
func selfAnnouncement(token string) bool {
	switch token {
	case "presence", "topics", "caps":
		return true
	}
	return false
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
	if len(t) == 4 && selfAnnouncement(t[3]) { // what a machine says about itself
		return Inbound{Kind: t[3], From: t[2]}, true
	}
	switch {
	case len(t) == 5 && t[3] == "out" && t[4] == n.Me:
		return Inbound{Kind: "out", From: t[2], To: n.Me}, true
	case len(t) == 5 && t[3] == "pub" && ValidName(t[4]):
		return Inbound{Kind: "pub", From: t[2], Arg: t[4]}, true
	}
	return Inbound{}, false
}
