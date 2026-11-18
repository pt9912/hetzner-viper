// Package chatlog is the port of the history of the chat (docs/plan/ui-chat.md §3.1): the messages of the conversations of a group,
// kept on this computer.
package chatlog

import "time"

// Record is one message. N is a number that grows within the group and does not change (the page asks "after N" to load what it
// missed, and "read" is the last N that was seen); T is the time it arrived here (UTC): the sender gives no time, and a sender cannot
// forge one.
type Record struct {
	N       int64     `json:"n"`
	T       time.Time `json:"t"`
	Dir     string    `json:"dir"` // in: for the operator or between others; out: the operator sent it
	From    string    `json:"from,omitempty"`
	To      string    `json:"to,omitempty"`
	Topic   string    `json:"topic,omitempty"`
	Type    string    `json:"type"` // msg, req, res
	ID      string    `json:"id,omitempty"`
	Re      string    `json:"re,omitempty"`
	Text    string    `json:"text,omitempty"`
	AttID   string    `json:"att_id,omitempty"`
	AttType string    `json:"att_type,omitempty"`
	AttName string    `json:"att_name,omitempty"`
	AttSize int64     `json:"att_size,omitempty"`
	AttSHA  string    `json:"att_sha256,omitempty"`
	Unknown bool      `json:"unknown,omitempty"` // a name that is no machine of the group
}

// Conversation is what the list shows of one conversation.
type Conversation struct {
	Key    string `json:"key"`
	Last   Record `json:"last"`
	Unread int    `json:"unread"`
}

// Hit is a record that a search found, with the conversation it is in.
type Hit struct {
	Key    string `json:"key"`
	Record Record `json:"record"`
}

// Store keeps the history.
type Store interface {
	// Append gives the record the next N of the group and writes it into each of the conversations.
	Append(group string, keys []string, r Record) (Record, error)
	// Tail is the last limit records of a conversation, oldest first; After the records with a larger N (at most limit, the oldest of them).
	Tail(group, key string, limit int) ([]Record, error)
	After(group, key string, n int64, limit int) ([]Record, error)
	// Conversations lists the conversations of a group, the one with the newest message first.
	Conversations(group string) ([]Conversation, error)
	// MarkRead says that the person has seen everything up to n; Read is that number.
	MarkRead(group, key string, n int64) error
	Read(group, key string) (int64, error)
	// Delete removes a conversation (key "": all of the group).
	Delete(group, key string) error
	// Search finds records whose text has the word (any case), the newest first, at most limit.
	Search(group, word string, limit int) ([]Hit, error)
	// Prune removes what is older than the time or over the limit of size; it is called now and then.
	Prune(group string, now time.Time) error
	// Close writes what is held back and frees the files.
	Close() error
}
