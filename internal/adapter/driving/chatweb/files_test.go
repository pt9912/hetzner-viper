package chatweb_test

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	chatlogfile "github.com/pt9912/hetzner-viper/internal/adapter/driven/chatlog"
	"github.com/pt9912/hetzner-viper/internal/adapter/driving/chatweb"
	"github.com/pt9912/hetzner-viper/internal/adapter/driving/web"
	port "github.com/pt9912/hetzner-viper/internal/port/driven/chatlog"
)

func pngBytes() []byte { return append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 100)...) }

func fileWorld(t *testing.T, b *fakeBackend, readonly bool) (*web.Server, http.Handler, map[string]string, *fakeChat) {
	t.Helper()
	g := chatweb.Group{Name: "dev", Operator: true, OperatorName: "operator", FileMax: 4096, Machines: []chatweb.Machine{{Name: "a"}}}
	nofiles := chatweb.Group{Name: "plain", Operator: true, OperatorName: "operator", Machines: []chatweb.Machine{{Name: "x"}}}
	b.groups = []chatweb.Group{g, nofiles}
	fc := newFakeChat()
	fc.store = chatlogfile.Memory(0)
	s, h := serve(t, *b, chatweb.Options{Engine: fc, Store: fc.store, Readonly: readonly})
	c := login(t, h, s)
	return s, h, map[string]string{"Cookie": c, "X-Viper-UI": "1", "Origin": "http://" + s.Host()}, fc
}

func up(h http.Handler, s *web.Server, hdr map[string]string, query string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/chat/upload?"+query, bytes.NewReader(body))
	r.Host = s.Host()
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	r.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func jsonHdr(hdr map[string]string) map[string]string {
	out := map[string]string{"Content-Type": "application/json"}
	for k, v := range hdr {
		out[k] = v
	}
	return out
}

func idOf(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `"id":"`)
	if i < 0 {
		t.Fatalf("no id in %s", body)
	}
	rest := body[i+6:]
	return rest[:strings.Index(rest, `"`)]
}

func TestAFileIsLookedAtBeforeItWaitsAndCanBeSentWithTheTextOrAlone(t *testing.T) {
	var sent []chatweb.Outgoing
	s, h, hdr, fc := fileWorld(t, &fakeBackend{sent: &sent, attID: "obj1", attSHA: strings.Repeat("a", 64)}, false)
	for _, c := range []struct {
		name  string
		body  []byte
		query string
		want  int
	}{
		{"not a type that is sent", []byte("just text"), "group=dev&name=a.txt", 415},
		{"too big for the group", append(pngBytes(), bytes.Repeat([]byte{1}, 5000)...), "group=dev&name=a.png", 413},
		{"a group without an object store", pngBytes(), "group=plain&name=a.png", 409},
		{"a group that is not here", pngBytes(), "group=nope&name=a.png", 404},
		{"empty", nil, "group=dev&name=a.png", 400},
	} {
		if w := up(h, s, hdr, c.query, c.body); w.Code != c.want {
			t.Errorf("%s: %d %s", c.name, w.Code, w.Body)
		}
	}
	w := up(h, s, hdr, "group=dev&name=../../my%20pic.png", pngBytes())
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"mypic.png"`) || !strings.Contains(w.Body.String(), `"type":"png"`) || !strings.Contains(w.Body.String(), `"size":108`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	id := idOf(t, w.Body.String())
	w = send(h, s, "POST", "/api/chat/send", jsonHdr(hdr), `{"group":"dev","kind":"msg","to":"a","text":"","file":"`+id+`"}`)
	if w.Code != 200 || len(sent) != 1 || sent[0].File == nil || sent[0].File.Name != "mypic.png" {
		t.Fatalf("%d %s %+v", w.Code, w.Body, sent)
	}
	data, err := os.ReadFile(sent[0].File.Path) // it was there while the message went
	_ = data
	if err == nil {
		t.Error("the file stays after it was sent")
	}
	r := fc.added[0]
	if r.AttName != "mypic.png" || r.AttType != "png" || r.AttSize != 108 || r.AttID != "obj1" || r.AttSHA != strings.Repeat("a", 64) {
		t.Errorf("%+v", r)
	}
	if w := send(h, s, "POST", "/api/chat/send", jsonHdr(hdr), `{"group":"dev","kind":"msg","to":"a","text":"again","file":"`+id+`"}`); w.Code != http.StatusGone {
		t.Errorf("a file goes once: %d", w.Code)
	}
}

func TestAFileGoesWithAMessageNotWithAnAnswerAndAFileOfAnotherGroupIsNotTaken(t *testing.T) {
	var sent []chatweb.Outgoing
	s, h, hdr, _ := fileWorld(t, &fakeBackend{sent: &sent}, false)
	id := idOf(t, up(h, s, hdr, "group=dev&name=a.png", pngBytes()).Body.String())
	if w := send(h, s, "POST", "/api/chat/send", jsonHdr(hdr), `{"group":"plain","kind":"msg","to":"x","text":"x","file":"`+id+`"}`); w.Code != http.StatusGone {
		t.Errorf("the file of another group: %d", w.Code)
	}
	if w := send(h, s, "POST", "/api/chat/send", jsonHdr(hdr), `{"group":"dev","kind":"reply","to":"a","re":"`+strings.Repeat("ab", 8)+`","text":"x","file":"`+id+`"}`); w.Code != 400 {
		t.Errorf("an answer with a file: %d", w.Code)
	}
	if len(sent) != 0 {
		t.Errorf("%+v", sent)
	}
}

func TestOnlyTheOneWhoMayWritesHandsFilesOver(t *testing.T) {
	s, h, hdr, _ := fileWorld(t, &fakeBackend{}, true)
	if w := up(h, s, hdr, "group=dev&name=a.png", pngBytes()); w.Code != http.StatusForbidden {
		t.Errorf("read only: %d", w.Code)
	}
	s, h, hdr, _ = fileWorld(t, &fakeBackend{}, false)
	delete(hdr, "X-Viper-UI")
	if w := up(h, s, hdr, "group=dev&name=a.png", pngBytes()); w.Code != http.StatusForbidden {
		t.Errorf("without the guard: %d", w.Code)
	}
}

func TestADownloadIsFetchedOnceKeptAndAlwaysAnAttachment(t *testing.T) {
	var fetched []string
	s, h, hdr, fc := fileWorld(t, &fakeBackend{fetched: &fetched}, false)
	sha := strings.Repeat("c", 64)
	_, _ = fc.store.Append("dev", []string{"rechner__a"}, port.Record{Dir: "in", From: "a", Type: "msg", Text: "see", AttID: "obj7", AttSHA: sha, AttName: "re port<1>.pdf", AttType: "pdf", AttSize: 8})
	_, _ = fc.store.Append("dev", []string{"rechner__a"}, port.Record{Dir: "in", From: "a", Type: "msg", Text: "no file"})
	get := func(q string) *httptest.ResponseRecorder {
		return get(h, s, hdr["Cookie"], "/api/chat/file?group=dev&"+q)
	}
	for i := 0; i < 2; i++ {
		w := get("conv=rechner__a&n=1")
		if w.Code != 200 || w.Body.String() != "the file" {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		if got := w.Header(); got.Get("Content-Type") != "application/octet-stream" || got.Get("X-Content-Type-Options") != "nosniff" ||
			!strings.HasPrefix(got.Get("Content-Disposition"), "attachment; filename*=UTF-8''") || strings.ContainsAny(got.Get("Content-Disposition")[28:], "<> ") {
			t.Errorf("%v", got)
		}
	}
	if len(fetched) != 1 || fetched[0] != "obj7 "+sha {
		t.Errorf("fetched %v", fetched)
	}
	for q, want := range map[string]int{"conv=rechner__a&n=2": 404, "conv=rechner__a&n=99": 404, "conv=rechner__a&n=x": 400, "conv=&n=1": 400, "conv=../x&n=1": 404} {
		if w := get(q); w.Code != want {
			t.Errorf("%s: %d", q, w.Code)
		}
	}
}

func TestAFailedFetchIsSaidAndLeavesNothingInTheCache(t *testing.T) {
	var fetched []string
	s, h, hdr, fc := fileWorld(t, &fakeBackend{fetched: &fetched, fetchErr: errors.New("no such object")}, false)
	_, _ = fc.store.Append("dev", []string{"rechner__a"}, port.Record{Dir: "in", From: "a", Type: "msg", AttID: "obj7", AttSHA: strings.Repeat("c", 64), AttName: "a.pdf"})
	for i := 0; i < 2; i++ {
		if w := get(h, s, hdr["Cookie"], "/api/chat/file?group=dev&conv=rechner__a&n=1"); w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "no such object") {
			t.Errorf("%d %s", w.Code, w.Body)
		}
	}
	if len(fetched) != 2 {
		t.Errorf("a failure is not kept: %v", fetched)
	}
}

func TestTheSameFileIsFetchedOnceEvenWhenAskedTogether(t *testing.T) {
	var fetched []string
	s, h, hdr, fc := fileWorld(t, &fakeBackend{fetched: &fetched}, false)
	_, _ = fc.store.Append("dev", []string{"rechner__a"}, port.Record{Dir: "in", From: "a", Type: "msg", AttID: "obj7", AttSHA: strings.Repeat("c", 64), AttName: "a.pdf"})
	done := make(chan int, 4)
	for i := 0; i < 4; i++ {
		go func() { done <- get(h, s, hdr["Cookie"], "/api/chat/file?group=dev&conv=rechner__a&n=1").Code }()
	}
	for i := 0; i < 4; i++ {
		if code := <-done; code != 200 {
			t.Errorf("%d", code)
		}
	}
	if len(fetched) != 1 {
		t.Errorf("fetched %d times", len(fetched))
	}
}

func TestAFileThatIsNotYetThereCannotBeSentAndOneThatIsTakenBackIsGone(t *testing.T) {
	var sent []chatweb.Outgoing
	s, h, hdr, _ := fileWorld(t, &fakeBackend{sent: &sent}, false)
	id := idOf(t, up(h, s, hdr, "group=dev&name=a.png", pngBytes()).Body.String())
	if w := send(h, s, "POST", "/api/chat/drop", jsonHdr(hdr), `{"group":"plain","id":"`+id+`"}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := send(h, s, "POST", "/api/chat/send", jsonHdr(hdr), `{"group":"dev","kind":"msg","to":"a","text":"x","file":"`+id+`"}`); w.Code != 200 {
		t.Errorf("a drop of another group changed nothing: %d", w.Code)
	}
	id = idOf(t, up(h, s, hdr, "group=dev&name=b.png", pngBytes()).Body.String())
	if w := send(h, s, "POST", "/api/chat/drop", jsonHdr(hdr), `{"group":"dev","id":"`+id+`"}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := send(h, s, "POST", "/api/chat/send", jsonHdr(hdr), `{"group":"dev","kind":"msg","to":"a","text":"x","file":"`+id+`"}`); w.Code != http.StatusGone {
		t.Errorf("taken back: %d", w.Code)
	}
}
