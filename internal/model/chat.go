package model

import "strings"

// The kinds of conversation. "alle" is the stream of the whole group (read only).
const (
	ChatKindMachine = "rechner"
	ChatKindTopic   = "thema"
	ChatKindPair    = "paar"
	ChatKindAll     = "alle"
)

// ChatKey is the name of a conversation. The names of machines and topics are lowercase letters, digits and dashes, never an
// underscore, so that "__" separates them without doubt: paar__a__b is not the pair (a-b, c) of paar__a-b__c.
func ChatKey(kind string, names ...string) string {
	if kind == ChatKindAll {
		return ChatKindAll
	}
	return kind + "__" + strings.Join(names, "__")
}

// ChatPairKey is the key of the pair of two machines, the same in both directions.
func ChatPairKey(a, b string) string {
	if b < a {
		a, b = b, a
	}
	return ChatKey(ChatKindPair, a, b)
}

// ChatGroup is what the router needs to know of a group: the name of the operator and the machines (a name that is not among them
// makes no conversation).
type ChatGroup struct {
	Operator string
	Machines map[string]bool
	NoPairs  bool
}

// Status of the connection of a group.
const (
	ChatStatusConnecting = "connecting"
	ChatStatusConnected  = "connected"
	ChatStatusDisturbed  = "disturbed"
	ChatStatusDown       = "down" // the group is not running: nothing to connect to
	ChatStatusAuth       = "auth" // the key is not known: no new try until the person logs in again
)

// ChatGroupStatus is the state of the connection of a group, and what it dropped.
type ChatGroupStatus struct {
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	RetryIn int    `json:"retry_in,omitempty"` // seconds
	Dropped int    `json:"dropped"`            // lines over the limit
	Refused int    `json:"refused"`            // new topics over the limit
}

// ChatSnapshot is what the pages ask for besides the messages: the state of the connection, who is there, who receives which topic.
type ChatSnapshot struct {
	Status   ChatGroupStatus             `json:"status"`
	Machines map[string]ChatMachineState `json:"machines"`
	Topics   map[string]ChatTopicState   `json:"topics"`
}

// ChatMachineState is: here, gone or unknown, and when it was seen (seconds ago, -1: never).
type ChatMachineState struct {
	State    string `json:"state"`
	SeenAgo  int    `json:"seen_ago"`
	Reported bool   `json:"reported"` // it has told its topics
}

// ChatTopicState is who receives a topic, and whether that is known (every machine of the group has reported).
type ChatTopicState struct {
	Subscribers []string `json:"subscribers"`
	Known       bool     `json:"known"`
}
