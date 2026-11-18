package chatweb_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	chatlogfile "github.com/pt9912/hetzner-viper/internal/adapter/driven/chatlog"
	"github.com/pt9912/hetzner-viper/internal/adapter/driving/chatweb"
	"github.com/pt9912/hetzner-viper/internal/adapter/driving/web"
	"github.com/pt9912/hetzner-viper/internal/model"
	port "github.com/pt9912/hetzner-viper/internal/port/driven/chatlog"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

// fakeChat is the engine of the tests: it records what is asked and says what it is told.
type fakeChat struct {
	mu      sync.Mutex
	store   port.Store
	added   []port.Record
	told    []driving.ChatEvent
	ensured map[string]model.ChatGroup
	events  chan driving.ChatEvent
	stopped chan struct{}
}

func newFakeChat() *fakeChat {
	return &fakeChat{ensured: map[string]model.ChatGroup{}, events: make(chan driving.ChatEvent, 8), stopped: make(chan struct{})}
}

func (f *fakeChat) Ensure(name string, g model.ChatGroup) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensured[name] = g
}
func (*fakeChat) Retry(string) {}
func (*fakeChat) Snapshot(string, []string) model.ChatSnapshot {
	return model.ChatSnapshot{Status: model.ChatGroupStatus{Status: model.ChatStatusConnected},
		Machines: map[string]model.ChatMachineState{"a": {State: "here"}}, Topics: map[string]model.ChatTopicState{"build": {Subscribers: []string{"a"}, Known: true}}}
}
func (f *fakeChat) Listen() (events <-chan driving.ChatEvent, closeFn func()) {
	return f.events, func() { close(f.stopped) }
}
func (*fakeChat) Stop() {}
func (f *fakeChat) Add(_ string, _ []string, r port.Record) (port.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r.N = int64(len(f.added) + 1)
	f.added = append(f.added, r)
	return r, nil
}
func (f *fakeChat) Tell(ev driving.ChatEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.told = append(f.told, ev)
}

func api(t *testing.T) (*web.Server, http.Handler, string, *fakeChat, *chatlogfile.Store) {
	t.Helper()
	g := chatweb.Group{Name: "dev", Operator: true, OperatorName: "operator", Topics: []string{"build", "quiet"},
		Machines: []chatweb.Machine{{Name: "a"}, {Name: "b"}}}
	off := chatweb.Group{Name: "other", Machines: []chatweb.Machine{{Name: "x"}}}
	st := chatlogfile.Memory(0)
	fc := newFakeChat()
	s, h := serve(t, fakeBackend{groups: []chatweb.Group{g, off}}, chatweb.Options{Engine: fc, Store: st, NoPairs: false})
	return s, h, login(t, h, s), fc, st
}

func add(t *testing.T, st port.Store, keys []string, r port.Record) port.Record {
	t.Helper()
	r, err := st.Append("dev", keys, r)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAGroupWithTheKeyIsReadAndOneWithoutIsNot(t *testing.T) {
	s, h, c, fc, _ := api(t)
	if w := get(h, s, c, "/api/chat/state"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if _, ok := fc.ensured["dev"]; !ok || len(fc.ensured) != 1 {
		t.Errorf("read: %v", fc.ensured)
	}
	if g := fc.ensured["dev"]; g.Operator != "operator" || !g.Machines["a"] || !g.Machines["b"] {
		t.Errorf("the group of the router: %+v", g)
	}
}

func TestTheListHasEveryMachineAndTopicOfTheSettingsAndWhatOnlyTheHistoryKnows(t *testing.T) {
	s, h, c, _, st := api(t)
	add(t, st, []string{"rechner__a", "alle"}, port.Record{Dir: "in", From: "a", Type: "msg", Text: strings.Repeat("x", 300)})
	add(t, st, []string{"paar__a__b", "alle"}, port.Record{Dir: "in", From: "a", To: "b", Type: "msg", Text: "pair"})
	add(t, st, []string{"thema__gone", "alle"}, port.Record{Dir: "in", From: "b", Topic: "gone", Type: "msg", Text: "topic nobody lists"})
	w := get(h, s, c, "/api/chat/conversations?group=dev")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var l chatweb.Listing
	if err := json.Unmarshal(w.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	by := map[string]chatweb.Conversation{}
	var order []string
	for _, x := range l.Conversations {
		by[x.Key] = x
		order = append(order, x.Key)
	}
	for _, k := range []string{"alle", "rechner__a", "rechner__b", "thema__build", "thema__quiet", "thema__gone", "paar__a__b"} {
		if _, ok := by[k]; !ok {
			t.Errorf("%s is not in %v", k, order)
		}
	}
	if order[0] != "alle" || order[1] != "thema__gone" || order[2] != "paar__a__b" {
		t.Errorf("the newest first: %v", order)
	}
	if x := by["rechner__a"]; x.Unread != 1 || x.Kind != "rechner" || x.Name != "a" || x.Last == nil || len(x.Last.Text) > 150 || !strings.HasSuffix(x.Last.Text, " ...") {
		t.Errorf("a: %+v", x)
	}
	if x := by["paar__a__b"]; x.Kind != "paar" || x.Name != "a b" {
		t.Errorf("pair: %+v", x)
	}
	if by["thema__quiet"].Last != nil || l.Snapshot.Machines["a"].State != "here" || !l.Snapshot.Topics["build"].Known {
		t.Errorf("snapshot or empty topic: %+v %+v", by["thema__quiet"], l.Snapshot)
	}
	if w := get(h, s, c, "/api/chat/conversations?group=nope"); w.Code != 404 {
		t.Errorf("a group that is not here: %d", w.Code)
	}
}

func TestTheMessagesComeAsTheLastOnesOrAfterANumberAndReadingIsKept(t *testing.T) {
	s, h, c, _, st := api(t)
	for i := 0; i < 5; i++ {
		add(t, st, []string{"rechner__a"}, port.Record{Dir: "in", From: "a", Type: "msg", Text: "m"})
	}
	type answer struct {
		Records []port.Record `json:"records"`
		Read    int64         `json:"read"`
	}
	read := func(q string) answer {
		w := get(h, s, c, "/api/chat/messages?group=dev&conv=rechner__a"+q)
		var a answer
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &a) != nil {
			t.Fatalf("%s: %d %s", q, w.Code, w.Body)
		}
		return a
	}
	if a := read("&limit=2"); len(a.Records) != 2 || a.Records[0].N != 4 || a.Records[1].N != 5 {
		t.Errorf("the last two: %+v", a.Records)
	}
	if a := read("&after=3&limit=10"); len(a.Records) != 2 || a.Records[0].N != 4 {
		t.Errorf("after 3: %+v", a.Records)
	}
	if a := read("&after=99"); a.Records == nil || len(a.Records) != 0 {
		t.Errorf("nothing after: %#v", a.Records)
	}
	if w := get(h, s, c, "/api/chat/messages?group=dev&conv=../x"); w.Code != 400 {
		t.Errorf("a key that is no key: %d", w.Code)
	}
	cookie := map[string]string{"Cookie": c, "Content-Type": "application/json", "X-Viper-UI": "1", "Origin": "http://" + s.Host()}
	if w := send(h, s, "POST", "/api/chat/read", cookie, `{"group":"dev","conv":"rechner__a","n":4}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if a := read("&limit=1"); a.Read != 4 {
		t.Errorf("read: %d", a.Read)
	}
	if w := send(h, s, "POST", "/api/chat/read", cookie, `{"group":"dev","conv":"x/../y","n":4}`); w.Code != 400 {
		t.Errorf("a key that is no key: %d", w.Code)
	}
	if w := send(h, s, "POST", "/api/chat/read", map[string]string{"Cookie": c}, `{}`); w.Code != http.StatusForbidden {
		t.Errorf("without the guard: %d", w.Code)
	}
}

func send(h http.Handler, s *web.Server, method, path string, hdr map[string]string, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = s.Host()
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// The stream starts with the snapshot, carries the events of its group only and ends when the page goes.
func TestTheStreamHasTheSnapshotAndTheEventsOfItsGroupOnly(t *testing.T) {
	s, _, c, fc, _ := api(t)
	srv := httptest.NewUnstartedServer(s.Handler())
	srv.Config.Addr = s.Host()
	srv.Start()
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/api/chat/events?group=dev", nil)
	req.Host = s.Host()
	req.Header.Set("Cookie", c)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatal(resp.StatusCode, resp.Header)
	}
	rd := bufio.NewReader(resp.Body)
	line := func() string {
		l, err := rd.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(l)
	}
	if line() != "event: snapshot" || !strings.Contains(line(), `"state":"here"`) {
		t.Error("no snapshot first")
	}
	line()
	fc.events <- driving.ChatEvent{Group: "other", Kind: "message", Record: &port.Record{N: 1, Text: "not for this page"}}
	fc.events <- driving.ChatEvent{Group: "dev", Kind: "message", Keys: []string{"rechner__a"}, Record: &port.Record{N: 7, Text: "for this page"}}
	if l := line(); l != "event: message" {
		t.Fatal(l)
	}
	if l := line(); !strings.Contains(l, "for this page") || strings.Contains(l, "not for") {
		t.Error(l)
	}
	_ = resp.Body.Close()
	select {
	case <-fc.stopped:
	case <-time.After(3 * time.Second):
		t.Error("the listening does not end with the page")
	}
}

func TestTheParametersAreChecked(t *testing.T) {
	s, h, c, _, st := api(t)
	add(t, st, []string{"rechner__a"}, port.Record{Dir: "in", From: "a", Type: "msg", Text: "m"})
	for _, p := range []string{"/api/chat/messages?group=dev", "/api/chat/messages?group=dev&conv=rechner__a&after=abc", "/api/chat/messages?group=dev&conv=rechner__a&after=-1"} {
		if w := get(h, s, c, p); w.Code != 400 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	if w := get(h, s, c, "/api/chat/messages?group=nope&conv=rechner__a"); w.Code != 404 {
		t.Errorf("a group that is not here: %d", w.Code)
	}
	cookie := map[string]string{"Cookie": c, "Content-Type": "application/json", "X-Viper-UI": "1", "Origin": "http://" + s.Host()}
	for body, want := range map[string]int{`{"group":"nope","conv":"rechner__a","n":1}`: 404, `{"group":"dev","conv":"","n":1}`: 400} {
		if w := send(h, s, "POST", "/api/chat/read", cookie, body); w.Code != want {
			t.Errorf("%s: %d", body, w.Code)
		}
	}
	if w := send(h, s, "POST", "/api/chat/read", cookie, `{"group":"dev","conv":"rechner__a","n":999}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if n, _ := st.Read("dev", "rechner__a"); n != 1 { // not more than there is: what comes later stays unread
		t.Errorf("read up to %d", n)
	}
}

func sendWorld(t *testing.T, readonly bool, b *fakeBackend) (*web.Server, http.Handler, map[string]string, *fakeChat) {
	t.Helper()
	g := chatweb.Group{Name: "dev", Operator: true, OperatorName: "operator", Topics: []string{"build"},
		Machines: []chatweb.Machine{{Name: "a"}, {Name: "b", Topics: []string{"build"}, PushFromOperator: true}}}
	off := chatweb.Group{Name: "other", Machines: []chatweb.Machine{{Name: "x"}}}
	b.groups = []chatweb.Group{g, off}
	fc := newFakeChat()
	fc.store = chatlogfile.Memory(0)
	s, h := serve(t, *b, chatweb.Options{Engine: fc, Store: fc.store, Readonly: readonly})
	c := login(t, h, s)
	return s, h, map[string]string{"Cookie": c, "Content-Type": "application/json", "X-Viper-UI": "1", "Origin": "http://" + s.Host()}, fc
}

func TestAMessageIsSentKeptAndShownInItsConversationsAndInAll(t *testing.T) {
	var sent []chatweb.Outgoing
	s, h, hdr, fc := sendWorld(t, false, &fakeBackend{sent: &sent})
	w := send(h, s, "POST", "/api/chat/send", hdr, `{"group":"dev","kind":"msg","to":"a","text":"hello a"}`)
	if w.Code != 200 || len(sent) != 1 || sent[0].To != "a" || sent[0].Text != "hello a" {
		t.Fatalf("%d %s %+v", w.Code, w.Body, sent)
	}
	r := fc.added[0]
	if r.From != "operator" || r.To != "a" || r.Type != "msg" || r.Text != "hello a" {
		t.Errorf("%+v", r)
	}
	w = send(h, s, "POST", "/api/chat/send", hdr, `{"group":"dev","kind":"topic","topic":"quiet","text":"to all"}`)
	if w.Code != 200 || sent[1].Kind != "topic" || fc.added[1].Topic != "quiet" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestWhatIsWrongWithAMessageIsRefusedBeforeAnythingIsSent(t *testing.T) {
	var sent []chatweb.Outgoing
	s, h, hdr, _ := sendWorld(t, false, &fakeBackend{sent: &sent})
	for body, want := range map[string]int{
		`{"group":"dev","kind":"msg","to":"a","text":""}`:                                   400,
		`{"group":"dev","kind":"msg","to":"a","text":"   \n"}`:                              400,
		`{"group":"dev","kind":"msg","to":"zzz","text":"x"}`:                                400,
		`{"group":"dev","kind":"msg","to":"dev","text":"x"}`:                                400,
		`{"group":"dev","kind":"topic","topic":"A.b","text":"x"}`:                           400,
		`{"group":"dev","kind":"shout","to":"a","text":"x"}`:                                400,
		`{"group":"dev","kind":"reply","to":"a","re":"nope","text":"x"}`:                    400,
		`{"group":"dev","kind":"msg","to":"a","text":"` + strings.Repeat("x", 16385) + `"}`: 400,
		`{"group":"nope","kind":"msg","to":"a","text":"x"}`:                                 404,
		`{"group":"other","kind":"msg","to":"x","text":"x"}`:                                409, // not logged in there
		`not json`: 400,
	} {
		if w := send(h, s, "POST", "/api/chat/send", hdr, body); w.Code != want {
			t.Errorf("%.60s: %d %s", body, w.Code, w.Body)
		}
	}
	if len(sent) != 0 {
		t.Errorf("sent: %+v", sent)
	}
	if w := send(h, s, "POST", "/api/chat/send", map[string]string{"Cookie": hdr["Cookie"]}, `{}`); w.Code != http.StatusForbidden {
		t.Errorf("without the guard: %d", w.Code)
	}
}

func TestTheChatThatOnlyReadsSendsNothing(t *testing.T) {
	var sent []chatweb.Outgoing
	s, h, hdr, _ := sendWorld(t, true, &fakeBackend{sent: &sent})
	if w := send(h, s, "POST", "/api/chat/send", hdr, `{"group":"dev","kind":"msg","to":"a","text":"x"}`); w.Code != 403 || len(sent) != 0 {
		t.Errorf("%d %+v", w.Code, sent)
	}
}

func TestAMessageToAMachineWhoseAgentReadsItInTheSessionNeedsTheOK(t *testing.T) {
	var sent []chatweb.Outgoing
	s, h, hdr, _ := sendWorld(t, false, &fakeBackend{sent: &sent})
	for _, body := range []string{`{"group":"dev","kind":"msg","to":"b","text":"x"}`, `{"group":"dev","kind":"topic","topic":"build","text":"x"}`} {
		before := len(sent)
		w := send(h, s, "POST", "/api/chat/send", hdr, body)
		if w.Code != 409 || !strings.Contains(w.Body.String(), `"push":["b"]`) || len(sent) != before {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body)
		}
		if w := send(h, s, "POST", "/api/chat/send", hdr, strings.Replace(body, "}", `,"ack":["b"]}`, 1)); w.Code != 200 {
			t.Errorf("with the ok: %d %s", w.Code, w.Body)
		}
	}
	if len(sent) != 2 {
		t.Errorf("%+v", sent)
	}
}

func TestAQuestionReturnsItsIDAndTheEndOfTheWaitingIsTold(t *testing.T) {
	var sent []chatweb.Outgoing
	var done func(string, error)
	s, h, hdr, fc := sendWorld(t, false, &fakeBackend{sent: &sent, id: strings.Repeat("ab", 8), done: &done})
	w := send(h, s, "POST", "/api/chat/send", hdr, `{"group":"dev","kind":"ask","to":"a","text":"ping","timeout":500}`)
	if w.Code != 200 || sent[0].Timeout != 120*time.Second || fc.added[0].Type != "req" || fc.added[0].ID != strings.Repeat("ab", 8) {
		t.Fatalf("%d %s %+v %+v", w.Code, w.Body, sent, fc.added)
	}
	done(strings.Repeat("ab", 8), errors.New("no answer from a within 30 seconds"))
	fc.mu.Lock()
	told := append([]driving.ChatEvent(nil), fc.told...)
	fc.mu.Unlock()
	if len(told) != 1 || told[0].Kind != "ask_end" || told[0].Record.Re != strings.Repeat("ab", 8) || !strings.Contains(told[0].Record.Text, "no answer") {
		t.Errorf("%+v", told)
	}
	if w := send(h, s, "POST", "/api/chat/send", hdr, `{"group":"dev","kind":"reply","to":"a","re":"`+strings.Repeat("cd", 8)+`","text":"yes"}`); w.Code != 400 {
		t.Errorf("an answer to a question that nobody asked: %d", w.Code)
	}
	if _, err := fc.store.Append("dev", []string{"rechner__a"}, port.Record{Dir: "in", From: "a", To: "operator", Type: "req", ID: strings.Repeat("cd", 8), Text: "may I?"}); err != nil {
		t.Fatal(err)
	}
	w = send(h, s, "POST", "/api/chat/send", hdr, `{"group":"dev","kind":"reply","to":"a","re":"`+strings.Repeat("cd", 8)+`","text":"yes"}`)
	if w.Code != 200 || sent[1].Kind != "reply" || fc.added[1].Type != "res" || fc.added[1].Re != strings.Repeat("cd", 8) {
		t.Errorf("%d %s", w.Code, w.Body)
	}
}

func TestAGrownSetOfAgentsNeedsTheOKAgainAndAnEncodedTextThatIsTooLongIsRefused(t *testing.T) {
	var sent []chatweb.Outgoing
	s, h, hdr, _ := sendWorld(t, false, &fakeBackend{sent: &sent})
	if w := send(h, s, "POST", "/api/chat/send", hdr, `{"group":"dev","kind":"msg","to":"b","text":"x","ack":["zzz"]}`); w.Code != 409 {
		t.Errorf("an ok for another machine: %d", w.Code)
	}
	long, _ := json.Marshal(strings.Repeat("\x01", 6000)) // 6 bytes each once encoded
	if w := send(h, s, "POST", "/api/chat/send", hdr, `{"group":"dev","kind":"msg","to":"a","text":`+string(long)+`}`); w.Code != 400 || !strings.Contains(w.Body.String(), "encoded") {
		t.Errorf("%d %s", w.Code, w.Body)
	}
}

func TestAnErrorOfTheSendingIsShownAndNothingIsKept(t *testing.T) {
	s, h, hdr, fc := sendWorld(t, false, &fakeBackend{sendErr: errors.New("not connected to the NATS server")})
	w := send(h, s, "POST", "/api/chat/send", hdr, `{"group":"dev","kind":"msg","to":"a","text":"x"}`)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "not connected") || len(fc.added) != 0 {
		t.Errorf("%d %s %+v", w.Code, w.Body, fc.added)
	}
}

func TestSearchFindsAPartOfAWordInAnyCaseOnceAndTheNewestFirst(t *testing.T) {
	s, h, c, _, st := api(t)
	add(t, st, []string{"rechner__a", "alle"}, port.Record{Dir: "in", From: "a", Type: "msg", Text: "The Cache is EMPTY"})
	add(t, st, []string{"thema__build", "alle"}, port.Record{Dir: "in", From: "b", Topic: "build", Type: "msg", Text: "cachet " + strings.Repeat("x", 400)})
	add(t, st, []string{"rechner__b", "alle"}, port.Record{Dir: "in", From: "b", Type: "msg", Text: "nothing"})
	w := get(h, s, c, "/api/chat/search?group=dev&q=ACH")
	var out struct{ Hits []chatweb.Hit }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Hits) != 2 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if out.Hits[0].Key != "thema__build" || out.Hits[0].Kind != "thema" || out.Hits[0].Name != "build" || len(out.Hits[0].Record.Text) > 310 || out.Hits[1].Key != "rechner__a" {
		t.Errorf("%+v", out.Hits)
	}
	for _, q := range []string{"", "a", "%20%20", strings.Repeat("a", 201)} {
		if w := get(h, s, c, "/api/chat/search?group=dev&q="+q); w.Code != 400 {
			t.Errorf("%q: %d", q, w.Code)
		}
	}
	if w := get(h, s, c, "/api/chat/search?group=nope&q=cache"); w.Code != 404 {
		t.Errorf("%d", w.Code)
	}
}

func TestADeleteRemovesOneConversationOrTheWholeHistoryAndNotWhenItOnlyReads(t *testing.T) {
	s, h, c, _, st := api(t)
	add(t, st, []string{"rechner__a", "alle"}, port.Record{Dir: "in", From: "a", Type: "msg", Text: "one"})
	add(t, st, []string{"rechner__b", "alle"}, port.Record{Dir: "in", From: "b", Type: "msg", Text: "two"})
	hdr := map[string]string{"Cookie": c, "Content-Type": "application/json", "X-Viper-UI": "1", "Origin": "http://" + s.Host()}
	if w := send(h, s, "POST", "/api/chat/delete", hdr, `{"group":"dev","conv":"rechner__a"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if got, _ := st.Tail("dev", "rechner__a", 10); len(got) != 0 {
		t.Errorf("not deleted: %v", got)
	}
	if got, _ := st.Tail("dev", "rechner__b", 10); len(got) != 1 {
		t.Errorf("the other one is gone: %v", got)
	}
	for body, want := range map[string]int{`{"group":"dev","conv":"../x"}`: 400, `{"group":"nope","conv":""}`: 404, `x`: 400} {
		if w := send(h, s, "POST", "/api/chat/delete", hdr, body); w.Code != want {
			t.Errorf("%s: %d", body, w.Code)
		}
	}
	if w := send(h, s, "POST", "/api/chat/delete", hdr, `{"group":"dev","conv":""}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if cs, _ := st.Conversations("dev"); len(cs) != 0 {
		t.Errorf("everything: %v", cs)
	}
}

func TestAChatThatOnlyReadsDeletesNothing(t *testing.T) {
	s, h, hdr, _ := sendWorld(t, true, &fakeBackend{})
	if w := send(h, s, "POST", "/api/chat/delete", hdr, `{"group":"dev","conv":""}`); w.Code != http.StatusForbidden {
		t.Errorf("%d", w.Code)
	}
}

func TestWhatAMachineWritesOnTopicsCanBeShownInItsConversation(t *testing.T) {
	s, h, c, _, st := api(t)
	add(t, st, []string{"rechner__a", "alle"}, port.Record{Dir: "in", From: "a", Type: "msg", Text: "direct"})                         // 1
	add(t, st, []string{"thema__build", "alle"}, port.Record{Dir: "in", From: "a", Topic: "build", Type: "msg", Text: "a on build"})   // 2
	add(t, st, []string{"thema__build", "alle"}, port.Record{Dir: "in", From: "b", Topic: "build", Type: "msg", Text: "b on build"})   // 3
	add(t, st, []string{"thema__quiet", "alle"}, port.Record{Dir: "out", From: "operator", Topic: "quiet", Type: "msg", Text: "mine"}) // 4
	add(t, st, []string{"thema__quiet", "alle"}, port.Record{Dir: "in", From: "a", Topic: "quiet", Type: "msg", Text: "a on quiet"})   // 5
	ns := func(q string) []int64 {
		w := get(h, s, c, "/api/chat/messages?group=dev&conv=rechner__a"+q)
		var out struct{ Records []port.Record }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("%s: %d %s", q, w.Code, w.Body)
		}
		var n []int64
		for _, r := range out.Records {
			n = append(n, r.N)
		}
		return n
	}
	eq := func(got []int64, want ...int64) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%v, want %v", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%v, want %v", got, want)
			}
		}
	}
	eq(ns(""), 1)                          // as before
	eq(ns("&topics=1"), 1, 2, 5)           // its own words on topics, by the order of arrival; not those of b, not those of the operator
	eq(ns("&topics=1&after=1"), 2, 5)      // what is newer than a number
	eq(ns("&topics=1&limit=2"), 2, 5)      // the last ones
	eq(ns("&topics=1&after=1&limit=1"), 2) // the oldest of the newer
	if w := get(h, s, c, "/api/chat/messages?group=dev&conv=thema__build&topics=1"); w.Code != 200 || strings.Contains(w.Body.String(), "a on quiet") {
		t.Errorf("a topic conversation takes no more: %d %s", w.Code, w.Body)
	}
}
