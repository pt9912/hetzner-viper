package chatweb_test

import (
	"bufio"
	"encoding/json"
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
