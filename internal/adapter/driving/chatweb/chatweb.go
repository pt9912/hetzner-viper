// Package chatweb is the chat of viper ui-chat (docs/plan/ui-chat.md): the pages and the programme interface that the web server of
// viper ui carries when it is made a chat (web.Chat). It knows the groups of NATS and their machines through a Backend; the streams,
// the history and the sending come with the later stages of the plan.
package chatweb

import (
	"context"
	"net/http"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/web"
)

// Machine is a machine of a NATS group, as the settings describe it.
type Machine struct {
	Name string `json:"name"`
	// Topics are the topics it subscribes to when it starts (NATS_MCP_TOPICS).
	Topics []string `json:"topics"`
	// PushFromOperator says that its agent gets the text of the operator pushed into the session (NATS_MCP_PUSH_FROM names the operator).
	PushFromOperator bool `json:"push_from_operator"`
}

// Group is a NATS group: its machines, and whether this computer has the key of the operator.
type Group struct {
	Name         string    `json:"name"`
	Operator     bool      `json:"operator"`      // the key is here
	OperatorName string    `json:"operator_name"` // the name the operator sends under (NATS_OPERATOR_NAME)
	FileMax      int64     `json:"file_max"`      // the largest file the group takes (0: no object store)
	Machines     []Machine `json:"machines"`
	Topics       []string  `json:"topics"` // the topics of the settings of all machines
}

// Backend is what the chat needs to know of the world.
type Backend interface {
	// Groups are the NATS groups of this computer.
	Groups(ctx context.Context) ([]Group, error)
}

// Options are the choices of the command line.
type Options struct {
	Readonly bool
	NoPairs  bool
	Version  string
}

// State is what the page loads first.
type State struct {
	Version  string  `json:"version"`
	Readonly bool    `json:"readonly"`
	NoPairs  bool    `json:"no_pairs"`
	Groups   []Group `json:"groups"`
}

// New makes the chat for the web server: the pages and the routes.
func New(b Backend, o Options) *web.Chat {
	return &web.Chat{
		Site: pages(),
		Handle: func(mux *http.ServeMux) {
			mux.HandleFunc("GET /api/chat/state", func(w http.ResponseWriter, r *http.Request) {
				gs, err := b.Groups(r.Context())
				if err != nil {
					web.Fail(w, http.StatusInternalServerError, err.Error())
					return
				}
				if gs == nil {
					gs = []Group{}
				}
				web.Reply(w, State{Version: o.Version, Readonly: o.Readonly, NoPairs: o.NoPairs, Groups: gs})
			})
		},
	}
}
