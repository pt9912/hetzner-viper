// Package chatweb is the chat of viper ui-chat (docs/plan/ui-chat.md): the pages and the programme interface that the web server of
// viper ui carries when it is made a chat (web.Chat). It knows the groups of NATS and their machines through a Backend, the history
// through a store and the streams through the engine.
package chatweb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/web"
	"github.com/pt9912/hetzner-viper/internal/model"
	port "github.com/pt9912/hetzner-viper/internal/port/driven/chatlog"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
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
	// Engine reads the streams, Store is the history.
	Engine driving.Chat
	Store  port.Store
}

// State is what the page loads first.
type State struct {
	Version  string  `json:"version"`
	Readonly bool    `json:"readonly"`
	NoPairs  bool    `json:"no_pairs"`
	Groups   []Group `json:"groups"`
}

// Conversation is an entry of the list: what the settings and the history know of it.
type Conversation struct {
	Key    string       `json:"key"`
	Kind   string       `json:"kind"`
	Name   string       `json:"name"` // the machine, the topic, "a b" for a pair
	Unread int          `json:"unread"`
	Last   *port.Record `json:"last,omitempty"`
}

// Listing is the answer to the list of conversations of a group.
type Listing struct {
	Snapshot      model.ChatSnapshot `json:"snapshot"`
	Conversations []Conversation     `json:"conversations"`
}

func (o Options) route(g Group) model.ChatGroup {
	m := map[string]bool{}
	for _, x := range g.Machines {
		m[x.Name] = true
	}
	return model.ChatGroup{Operator: g.OperatorName, Machines: m, NoPairs: o.NoPairs}
}

// New makes the chat for the web server: the pages and the routes.
func New(b Backend, o Options) *web.Chat {
	h := &handlers{b: b, o: o}
	return &web.Chat{
		Site: pages(),
		Handle: func(mux *http.ServeMux, done <-chan struct{}) {
			mux.HandleFunc("GET /api/chat/state", h.state)
			mux.HandleFunc("GET /api/chat/conversations", h.conversations)
			mux.HandleFunc("GET /api/chat/messages", h.messages)
			mux.HandleFunc("POST /api/chat/read", h.markRead)
			mux.HandleFunc("GET /api/chat/events", func(w http.ResponseWriter, r *http.Request) { h.events(w, r, done) })
		},
		Streams: []string{"/api/chat/events"},
	}
}

type handlers struct {
	b Backend
	o Options
}

func (h *handlers) groups(r *http.Request) ([]Group, error) {
	gs, err := h.b.Groups(r.Context())
	if err != nil {
		return nil, err
	}
	for _, g := range gs { // a group of which the operator has the key is read, from now on
		if g.Operator && h.o.Engine != nil {
			h.o.Engine.Ensure(g.Name, h.o.route(g))
		}
	}
	return gs, nil
}

func (h *handlers) group(w http.ResponseWriter, r *http.Request) (Group, bool) {
	return h.named(w, r, r.URL.Query().Get("group"))
}

// named is the group of that name, if it is one of this computer; else the answer is given.
func (h *handlers) named(w http.ResponseWriter, r *http.Request, name string) (Group, bool) {
	if h.o.Store == nil {
		web.Fail(w, http.StatusNotFound, "no history")
		return Group{}, false
	}
	gs, err := h.groups(r)
	if err != nil {
		web.Fail(w, http.StatusInternalServerError, err.Error())
		return Group{}, false
	}
	for _, g := range gs {
		if g.Name == name {
			return g, true
		}
	}
	web.Fail(w, http.StatusNotFound, "no such group")
	return Group{}, false
}

func (h *handlers) state(w http.ResponseWriter, r *http.Request) {
	gs, err := h.groups(r)
	if err != nil {
		web.Fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if gs == nil {
		gs = []Group{}
	}
	web.Reply(w, State{Version: h.o.Version, Readonly: h.o.Readonly, NoPairs: h.o.NoPairs, Groups: gs})
}

// conversations merges what the settings say (every machine, every topic) with the history (also topics and pairs that only the
// history knows), the newest first, and adds who is there.
func (h *handlers) conversations(w http.ResponseWriter, r *http.Request) {
	g, ok := h.group(w, r)
	if !ok {
		return
	}
	have := map[string]Conversation{}
	add := func(kind, name string) {
		key := model.ChatKey(kind, strings.Fields(name)...)
		if kind == model.ChatKindPair {
			p := strings.Fields(name)
			key = model.ChatPairKey(p[0], p[1])
		}
		if _, ok := have[key]; !ok {
			have[key] = Conversation{Key: key, Kind: kind, Name: name}
		}
	}
	add(model.ChatKindAll, "")
	for _, m := range g.Machines {
		add(model.ChatKindMachine, m.Name)
	}
	for _, t := range g.Topics {
		add(model.ChatKindTopic, t)
	}
	if h.o.Store != nil {
		cs, err := h.o.Store.Conversations(g.Name)
		if err != nil {
			web.Fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, c := range cs {
			x := have[c.Key]
			x.Key, x.Kind, x.Name = c.Key, kindOf(c.Key), nameOf(c.Key)
			x.Unread = c.Unread
			last := c.Last
			last.Text = cut(last.Text, 140)
			x.Last = &last
			have[c.Key] = x
		}
	}
	out := make([]Conversation, 0, len(have))
	for _, c := range have {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := int64(0), int64(0)
		if out[i].Last != nil {
			a = out[i].Last.N
		}
		if out[j].Last != nil {
			b = out[j].Last.N
		}
		if a != b {
			return a > b
		}
		return out[i].Key < out[j].Key
	})
	var snap model.ChatSnapshot
	if h.o.Engine != nil {
		snap = h.o.Engine.Snapshot(g.Name, g.Topics)
	}
	web.Reply(w, Listing{Snapshot: snap, Conversations: out})
}

func kindOf(key string) string {
	if key == model.ChatKindAll {
		return model.ChatKindAll
	}
	k, _, _ := strings.Cut(key, "__")
	return k
}

func nameOf(key string) string {
	_, rest, _ := strings.Cut(key, "__")
	return strings.ReplaceAll(rest, "__", " ")
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n] + " ..."
}

// messages is the last records of a conversation (limit), or the ones after a number (after): what a page asks for when it opens
// a conversation and when it has missed something.
func (h *handlers) messages(w http.ResponseWriter, r *http.Request) {
	g, ok := h.group(w, r)
	if !ok {
		return
	}
	key := r.URL.Query().Get("conv")
	if key == "" {
		web.Fail(w, http.StatusBadRequest, "no conversation")
		return
	}
	limit := atoi(r.URL.Query().Get("limit"), 100, 1, 500)
	var recs []port.Record
	var err error
	if a := r.URL.Query().Get("after"); a != "" {
		n, perr := strconv.ParseInt(a, 10, 64)
		if perr != nil || n < 0 {
			web.Fail(w, http.StatusBadRequest, "after is a number")
			return
		}
		recs, err = h.o.Store.After(g.Name, key, n, limit)
	} else {
		recs, err = h.o.Store.Tail(g.Name, key, limit)
	}
	if err != nil {
		web.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if recs == nil {
		recs = []port.Record{}
	}
	read, _ := h.o.Store.Read(g.Name, key)
	web.Reply(w, map[string]any{"records": recs, "read": read})
}

func atoi(s string, def, lo, hi int) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < lo {
		return def
	}
	return min(n, hi)
}

func (h *handlers) markRead(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Group string `json:"group"`
		Conv  string `json:"conv"`
		N     int64  `json:"n"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		web.Fail(w, http.StatusBadRequest, "bad request")
		return
	}
	g, ok := h.named(w, r, req.Group)
	if !ok {
		return
	}
	if req.Conv == "" {
		web.Fail(w, http.StatusBadRequest, "no conversation")
		return
	}
	last, err := h.o.Store.Tail(g.Name, req.Conv, 1)
	if err != nil {
		web.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(last) == 0 { // nothing to have read
		web.Reply(w, map[string]bool{"ok": true})
		return
	}
	if err := h.o.Store.MarkRead(g.Name, req.Conv, min(req.N, last[0].N)); err != nil { // not more than there is: new messages stay unread
		web.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	web.Reply(w, map[string]bool{"ok": true})
}

// events streams the messages that arrive and the changes of the state of one group, and the snapshot (who is there) every five
// seconds. A page that has missed something asks for what it lacks by the numbers; the stream carries no history.
func (h *handlers) events(w http.ResponseWriter, r *http.Request, done <-chan struct{}) {
	g, ok := h.group(w, r)
	if !ok {
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok || h.o.Engine == nil {
		web.Fail(w, http.StatusNotFound, "no stream")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	events, stop := h.o.Engine.Listen()
	defer stop()
	send := func(event string, v any) bool {
		b, _ := json.Marshal(v)
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	if !send("snapshot", h.o.Engine.Snapshot(g.Name, g.Topics)) {
		return
	}
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case ev := <-events:
			if ev.Group != g.Name {
				continue
			}
			if !send(ev.Kind, ev) {
				return
			}
		case <-tick.C:
			if !send("snapshot", h.o.Engine.Snapshot(g.Name, g.Topics)) {
				return
			}
		case <-r.Context().Done():
			return
		case <-done:
			return
		}
	}
}
