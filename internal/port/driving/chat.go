package driving

import (
	"github.com/pt9912/hetzner-viper/internal/model"
	port "github.com/pt9912/hetzner-viper/internal/port/driven/chatlog"
)

// ChatEvent is what the pages of the chat get: a message that was kept, or a change of the state.
type ChatEvent struct {
	Group  string                 `json:"group"`
	Kind   string                 `json:"kind"` // message, status
	Keys   []string               `json:"keys,omitempty"`
	Record *port.Record           `json:"record,omitempty"`
	Status *model.ChatGroupStatus `json:"status,omitempty"`
}

// Chat reads the streams of the NATS groups, keeps what comes in and tells the pages (viper ui-chat).
type Chat interface {
	// Ensure starts reading a group if it is not read yet; the machines of a group that is read are updated.
	Ensure(name string, g model.ChatGroup)
	// Retry makes a group that waits for a login try again.
	Retry(name string)
	// Snapshot is the state of the connection of a group, who is there and who receives which topic.
	Snapshot(name string, topics []string) model.ChatSnapshot
	// Listen returns the events from now on, and the function that ends the listening.
	Listen() (events <-chan ChatEvent, closeFn func())
	// Add keeps a message of the operator in the history (the stream does not bring it back) and tells the pages.
	Add(name string, keys []string, rec port.Record) (port.Record, error)
	// Tell gives an event to the pages.
	Tell(ev ChatEvent)
	// Stop ends the reading of all groups.
	Stop()
}
