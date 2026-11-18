// Package chat is the logic of the chat of viper ui-chat (docs/plan/ui-chat.md): which conversations a line of the stream belongs to,
// who is there, how fast lines are taken and which new conversations are allowed. It knows nothing of NATS, files or the web.
package chat

import (
	"strings"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/chatstream"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

// Group is what the router needs to know of a group.
type Group = model.ChatGroup

// GroupStatus is the state of the connection of a group.
type GroupStatus = model.ChatGroupStatus

// Snapshot is who is there and who receives which topic.
type Snapshot = model.ChatSnapshot

// MachineState is the state of a machine in a Snapshot.
type MachineState = model.ChatMachineState

// TopicState is the receivers of a topic in a Snapshot.
type TopicState = model.ChatTopicState

// Event is what the pages get.
type Event = driving.ChatEvent

// Conn is the connection of the operator to the NATS server of a group.
type Conn = chatstream.Conn

// Connector opens connections.
type Connector = chatstream.Connector

// The kinds of conversation and the states of a connection.
const (
	KindMachine = model.ChatKindMachine
	KindTopic   = model.ChatKindTopic
	KindPair    = model.ChatKindPair
	KindAll     = model.ChatKindAll

	StatusConnecting = model.ChatStatusConnecting
	StatusConnected  = model.ChatStatusConnected
	StatusDisturbed  = model.ChatStatusDisturbed
	StatusAuth       = model.ChatStatusAuth
)

// ErrAuth is the reason of a connection that does not come up because the server does not know the key.
var ErrAuth = chatstream.ErrAuth

// Key is the name of a conversation.
func Key(kind string, names ...string) string { return model.ChatKey(kind, names...) }

// PairKey is the key of the pair of two machines, the same in both directions.
func PairKey(a, b string) string { return model.ChatPairKey(a, b) }

// Route is where a line of the stream goes.
type Route struct {
	Keys    []string // the conversations, "alle" included
	Unknown bool     // a name that is no machine of the group (it is in "alle" only)
	Skip    bool     // no message (presence, the list of topics, ready ...)
}

// Classify says which conversations a message line belongs to. Presence and topic lists are no messages: Skip.
func Classify(g Group, l model.NATSLine) Route {
	if l.Event != "message" {
		return Route{Skip: true}
	}
	switch l.Via {
	case "presence", "topics":
		return Route{Skip: true}
	case "pub":
		if !g.Machines[l.From] || model.CheckNATSName("topic", l.Topic) != nil {
			return Route{Keys: []string{KindAll}, Unknown: true}
		}
		return Route{Keys: []string{Key(KindTopic, l.Topic), KindAll}}
	case "out":
		return classifyDirect(g, l)
	}
	return Route{Skip: true}
}

func classifyDirect(g Group, l model.NATSLine) Route {
	fromM, toM := g.Machines[l.From], g.Machines[l.To]
	switch {
	case toM && l.From == g.Operator:
		return Route{Keys: []string{Key(KindMachine, l.To), KindAll}}
	case fromM && l.To == g.Operator:
		return Route{Keys: []string{Key(KindMachine, l.From), KindAll}}
	case fromM && toM && l.From != l.To:
		if g.NoPairs {
			return Route{Keys: []string{KindAll}}
		}
		return Route{Keys: []string{PairKey(l.From, l.To), KindAll}}
	}
	return Route{Keys: []string{KindAll}, Unknown: true}
}

// Presence is who has said that they are there, and which topics they receive. A machine that has not said anything is unknown (it may
// have no session, or an older version), not away.
type Presence struct {
	seen   map[string]time.Time
	bye    map[string]bool
	topics map[string][]string
	ttl    time.Duration
}

// NewPresence makes the table; ttl is how long "I am here" counts (90 seconds in the group).
func NewPresence(ttl time.Duration) *Presence {
	return &Presence{seen: map[string]time.Time{}, bye: map[string]bool{}, topics: map[string][]string{}, ttl: ttl}
}

// Update takes a line of presence (via presence) or of the topics of a machine (via topics); other lines change nothing.
func (p *Presence) Update(l model.NATSLine, now time.Time) {
	if l.Event != "message" || l.From == "" {
		return
	}
	switch l.Via {
	case "presence":
		if l.State == "bye" {
			p.bye[l.From] = true
			return
		}
		p.seen[l.From], p.bye[l.From] = now, false
	case "topics":
		p.topics[l.From] = strings.Fields(l.Text)
	}
}

// State of a machine: "here" (said so within the time), "gone" (said bye, or said so long ago), "unknown" (never said anything).
func (p *Presence) State(machine string, now time.Time) string {
	t, ok := p.seen[machine]
	switch {
	case !ok && !p.bye[machine]:
		return "unknown"
	case p.bye[machine] || now.Sub(t) > p.ttl:
		return "gone"
	}
	return "here"
}

// LastSeen is when the machine said that it is there (zero: never).
func (p *Presence) LastSeen(machine string) time.Time { return p.seen[machine] }

// Subscribers are the machines that have said that they receive the topic.
func (p *Presence) Subscribers(topic string) []string {
	var out []string
	for m, ts := range p.topics {
		for _, t := range ts {
			if t == topic {
				out = append(out, m)
				break
			}
		}
	}
	return out
}

// Reported says that the machine has told its topics at least once (before that, "niemand abonniert" is not known).
func (p *Presence) Reported(machine string) bool { _, ok := p.topics[machine]; return ok }
