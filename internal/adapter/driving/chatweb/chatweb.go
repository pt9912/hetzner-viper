// Package chatweb is the chat of viper ui-chat (docs/plan/ui-chat.md): the pages and the programme interface that the web server of
// viper ui carries when it is made a chat (web.Chat). It knows the groups of NATS and their machines through a Backend, the history
// through a store and the streams through the engine.
package chatweb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

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
	// Send sends what the operator wrote to the NATS server of the group, as viper nats does. For a question (Kind "ask") it returns when
	// the question is out, with its ID; done is called when the waiting is over (an error if no answer came: the answer itself comes
	// in the stream). For the others it returns when the message is handed to the server.
	Send(ctx context.Context, group string, o Outgoing, done func(id string, err error)) (Sent, error)
	// Fetch puts the file of a message (the ID of its object and its checksum, as the message names them) into the folder.
	Fetch(ctx context.Context, group, attID, sha, dir string) error
}

// Sent is what the sending knows of the message: the ID of a question, and the file as the server took it.
type Sent struct {
	ID                     string
	Via                    string // the server that took it: the machine that holds it, and its address
	AttID, AttType, AttSHA string
	AttSize                int64
}

// FileRef is a file that goes with a message: where it is on this computer, and its name.
type FileRef struct {
	Name, Path string
}

// Outgoing is a message of the operator.
type Outgoing struct {
	Kind    string // msg (to a machine), topic, ask (a question to a machine), reply (to the question of a machine)
	To      string
	Topic   string
	Text    string
	Re      string        // reply: the ID of the question
	File    *FileRef      // msg, topic, ask: a file with the text (or without)
	Timeout time.Duration // ask
}

// MaxText is the longest text of a message, in bytes.
const MaxText = 16384

// SendRequest is what the page sends.
type SendRequest struct {
	Group   string   `json:"group"`
	Kind    string   `json:"kind"`
	To      string   `json:"to"`
	Topic   string   `json:"topic"`
	Text    string   `json:"text"`
	Re      string   `json:"re"`
	Timeout int      `json:"timeout"`
	File    string   `json:"file"` // the ID that /api/chat/upload gave
	Ack     []string `json:"ack"`  // the machines of which the person has read that the text lands in the session of their agent
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
			mux.HandleFunc("POST /api/chat/send", h.send)
			mux.HandleFunc("POST /api/chat/upload", h.upload)
			mux.HandleFunc("POST /api/chat/drop", h.drop)
			mux.HandleFunc("GET /api/chat/search", h.search)
			mux.HandleFunc("POST /api/chat/delete", h.remove)
			mux.HandleFunc("GET /api/chat/file", h.file)
			go func() { <-done; h.files.close() }()
			mux.HandleFunc("GET /api/chat/events", func(w http.ResponseWriter, r *http.Request) { h.events(w, r, done) })
		},
		Streams: []string{"/api/chat/events"},
		Octet:   []string{"/api/chat/upload"},
	}
}

type handlers struct {
	b        Backend
	o        Options
	files    uploads
	inFlight atomic.Int32 // sends and questions that are not over
}

const maxInFlight = 8

var idPattern = regexp.MustCompile(`^[a-f0-9]{16,64}$`)

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

// pushers are the machines whose agent gets the text pushed into its session: for a machine that one, for a topic every machine that
// receives the topic and lists the operator in NATS_MCP_PUSH_FROM.
func pushers(g Group, o Outgoing) []string {
	var out []string
	for _, m := range g.Machines {
		if !m.PushFromOperator {
			continue
		}
		if o.Kind == "topic" && slices.Contains(m.Topics, o.Topic) || o.Kind != "topic" && m.Name == o.To {
			out = append(out, m.Name)
		}
	}
	return out
}

// check says what is wrong with a message, or returns it ready.
func (*handlers) check(g Group, req SendRequest) (Outgoing, string) {
	o := Outgoing{Kind: req.Kind, To: req.To, Topic: req.Topic, Text: req.Text, Re: req.Re}
	if bad := checkText(req); bad != "" {
		return o, bad
	}
	if req.Kind == "topic" {
		if err := model.CheckNATSName("topic", req.Topic); err != nil {
			return o, err.Error()
		}
		return o, ""
	}
	if req.Kind != "msg" && req.Kind != "ask" && req.Kind != "reply" {
		return o, "kind is msg, topic, ask or reply"
	}
	if !slices.ContainsFunc(g.Machines, func(m Machine) bool { return m.Name == req.To }) {
		return o, "no such machine in the group"
	}
	if req.Kind == "reply" && !idPattern.MatchString(req.Re) {
		return o, "re is the ID of a question"
	}
	if req.Kind == "ask" {
		o.Timeout = time.Duration(atoi(strconv.Itoa(req.Timeout), 30, 1, 120)) * time.Second
	}
	return o, ""
}

// checkText says what is wrong with the text of a message.
func checkText(req SendRequest) string {
	switch {
	case (len(req.Text) == 0 || strings.TrimSpace(req.Text) == "") && req.File == "":
		return "the text is empty"
	case req.File != "" && req.Kind == "reply":
		return "an answer takes no file"
	case len(req.Text) > MaxText:
		return fmt.Sprintf("the text has %d bytes, the most is %d", len(req.Text), MaxText)
	case !utf8.ValidString(req.Text):
		return "the text is not valid UTF-8"
	}
	if enc, _ := json.Marshal(req.Text); len(enc) > 30<<10 { // the server takes 32 KiB of the whole message; a control character is 6 bytes of it
		return "the text is too long once it is encoded (control characters and quotes take more room)"
	}
	return ""
}

// keysOf are the conversations a message of the operator belongs to.
func keysOf(o Outgoing) []string {
	if o.Kind == "topic" {
		return []string{model.ChatKey(model.ChatKindTopic, o.Topic), model.ChatKindAll}
	}
	return []string{model.ChatKey(model.ChatKindMachine, o.To), model.ChatKindAll}
}

// prepare reads and checks a request to send; when something is wrong the answer is given and ok is false.
func (h *handlers) prepare(w http.ResponseWriter, r *http.Request) (g Group, o Outgoing, up *upload, req SendRequest, ok bool) {
	if h.o.Readonly {
		web.Fail(w, http.StatusForbidden, "this page only reads (--readonly)")
		return g, o, nil, req, false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&req); err != nil {
		web.Fail(w, http.StatusBadRequest, "bad request")
		return g, o, nil, req, false
	}
	if g, ok = h.named(w, r, req.Group); !ok {
		return g, o, nil, req, false
	}
	if !g.Operator || h.o.Engine == nil {
		web.Fail(w, http.StatusConflict, "you are not logged in in this group")
		return g, o, nil, req, false
	}
	o, bad := h.check(g, req)
	if bad != "" {
		web.Fail(w, http.StatusBadRequest, bad)
		return g, o, nil, req, false
	}
	if req.File != "" {
		if up = h.files.get(g.Name, req.File); up == nil {
			web.Fail(w, http.StatusGone, "the file is gone: choose it again")
			return g, o, nil, req, false
		}
		o.File = &FileRef{Name: up.name, Path: up.path}
	}
	if o.Kind == "reply" && !h.asked(g.Name, o) {
		web.Fail(w, http.StatusBadRequest, "no open question of this machine with this ID")
		return g, o, nil, req, false
	}
	if p := pushers(g, o); slices.ContainsFunc(p, func(m string) bool { return !slices.Contains(req.Ack, m) }) { // one that was not acknowledged
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "the text lands in the session of an agent", "push": p})
		return g, o, nil, req, false
	}
	return g, o, up, req, true
}

func (h *handlers) send(w http.ResponseWriter, r *http.Request) {
	g, o, up, req, ok := h.prepare(w, r)
	if !ok {
		return
	}
	if up != nil {
		defer h.files.drop(req.File) // from here on the file goes: a refusal before this (the question of the agent) keeps it
	}
	if h.inFlight.Add(1) > maxInFlight {
		h.inFlight.Add(-1)
		web.Fail(w, http.StatusTooManyRequests, "too many messages are on their way")
		return
	}
	var once sync.Once
	end := func() { once.Do(func() { h.inFlight.Add(-1) }) }
	defer func() {
		if o.Kind != "ask" { // a question frees its place when the waiting is over
			end()
		}
	}()
	keys := keysOf(o)
	sent, err := h.dispatch(r.Context(), g.Name, o, keys, end)
	if err != nil {
		web.Fail(w, http.StatusBadGateway, err.Error())
		return
	}
	typ := map[string]string{"ask": "req", "reply": "res"}[o.Kind]
	if typ == "" {
		typ = "msg"
	}
	rec := port.Record{From: g.OperatorName, To: o.To, Topic: o.Topic, Type: typ, Text: o.Text, ID: sent.ID, Re: o.Re}
	if up != nil {
		rec.AttName, rec.AttType, rec.AttSize, rec.AttID, rec.AttSHA = up.name, up.typ, up.size, sent.AttID, sent.AttSHA
	}
	kept, err := h.o.Engine.Add(g.Name, keys, rec)
	if err != nil { // it is out: the person is told, and does not send it again
		web.Reply(w, map[string]any{"record": rec, "keys": keys, "via": sent.Via, "warning": "sent, but not kept in the history: " + err.Error()})
		return
	}
	web.Reply(w, map[string]any{"record": kept, "keys": keys, "via": sent.Via})
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// asked says whether the machine asked the question that is answered (it is in the history of its conversation).
func (h *handlers) asked(group string, o Outgoing) bool {
	recs, err := h.o.Store.Tail(group, model.ChatKey(model.ChatKindMachine, o.To), 500)
	return err == nil && slices.ContainsFunc(recs, func(r port.Record) bool {
		return r.Type == "req" && r.Dir == "in" && r.From == o.To && r.ID == o.Re
	})
}

// dispatch gives the message to the backend. A question frees its place (end) when the waiting is over, and tells the pages then.
func (h *handlers) dispatch(ctx context.Context, group string, o Outgoing, keys []string, end func()) (Sent, error) {
	if o.Kind != "ask" {
		return h.b.Send(ctx, group, o, nil)
	}
	sent, err := h.b.Send(ctx, group, o, func(askID string, err error) { // the answer, if any, came in the stream
		end()
		h.o.Engine.Tell(driving.ChatEvent{Group: group, Kind: "ask_end", Keys: keys, Record: &port.Record{Re: askID, Text: errText(err)}})
	})
	if err != nil {
		end()
	}
	return sent, err
}

// Hit is a message that has the word, and where it is.
type Hit struct {
	Key    string      `json:"key"`
	Kind   string      `json:"kind"`
	Name   string      `json:"name"`
	Record port.Record `json:"record"`
}

// maxHits is how many hits a search gives.
const maxHits = 200

// search is GET /api/chat/search?group=dev&q=word: the history of the group, any case, a part of a word is enough, the newest first.
// A message is found in its own conversation (not once more in "all").
func (h *handlers) search(w http.ResponseWriter, r *http.Request) {
	g, ok := h.group(w, r)
	if !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 200 {
		web.Fail(w, http.StatusBadRequest, "search for at most 200 bytes")
		return
	}
	if utf8.RuneCountInString(q) < 2 {
		web.Fail(w, http.StatusBadRequest, "search for at least 2 characters")
		return
	}
	found, err := h.o.Store.Search(g.Name, q, maxHits)
	if err != nil {
		web.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	hits := make([]Hit, 0, len(found))
	for _, f := range found {
		rec := f.Record
		rec.Text = cut(rec.Text, 300)
		hits = append(hits, Hit{Key: f.Key, Kind: kindOf(f.Key), Name: nameOf(f.Key), Record: rec})
	}
	web.Reply(w, map[string]any{"hits": hits})
}

// remove is POST /api/chat/delete {group, conv}: the history of one conversation, or (conv "") of the whole group. Only here:
// the others (NATS keeps nothing) are not touched. A page that only reads cannot.
func (h *handlers) remove(w http.ResponseWriter, r *http.Request) {
	if h.o.Readonly {
		web.Fail(w, http.StatusForbidden, "this page only reads (--readonly)")
		return
	}
	var req struct {
		Group string `json:"group"`
		Conv  string `json:"conv"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		web.Fail(w, http.StatusBadRequest, "bad request")
		return
	}
	g, ok := h.named(w, r, req.Group)
	if !ok {
		return
	}
	if err := h.o.Store.Delete(g.Name, req.Conv); err != nil {
		web.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	web.Reply(w, map[string]bool{"ok": true})
}
