package web_test

import (
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/web"
)

func chatServer(t *testing.T) (*web.Server, http.Handler) {
	t.Helper()
	cfg := web.Config{Version: "vtest", Chat: &web.Chat{
		Site: fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>chat</title>")}},
		Handle: func(mux *http.ServeMux, _ <-chan struct{}) {
			mux.HandleFunc("GET /api/chat/state", func(w http.ResponseWriter, r *http.Request) { web.Reply(w, map[string]string{"ok": "yes"}) })
		},
		Streams: []string{"/api/chat/events"},
	}}
	s := web.New(cfg)
	ln, err := s.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return s, s.Handler()
}

// The chat has the session of viper ui and its pages and routes only: the key of the chat must not start viper up or down, edit a
// settings file or read the settings.
func TestTheChatServesItsPagesAndItsRoutesAndNoneOfViperUi(t *testing.T) {
	s, h := chatServer(t)
	if w := do(h, "GET", s.Host(), "/", nil, ""); w.Code != http.StatusForbidden {
		t.Errorf("without the key: %d", w.Code)
	}
	c := login(t, s, h)
	if w := do(h, "GET", s.Host(), "/", c, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "<title>chat</title>") {
		t.Errorf("the page of the chat: %d %q", w.Code, w.Body)
	}
	if w := do(h, "GET", s.Host(), "/api/chat/state", c, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":"yes"`) {
		t.Errorf("the route of the chat: %d %q", w.Code, w.Body)
	}
	post := map[string]string{"Cookie": c["Cookie"], "Content-Type": "application/json", "X-Viper-UI": "1", "Origin": "http://" + s.Host()}
	for _, p := range []string{"/api/run", "/api/check", "/api/cancel", "/api/answer", "/api/runs/remove", "/api/edit/preview", "/api/edit/apply"} {
		if w := do(h, "POST", s.Host(), p, post, `{"action":"down","machine":"web"}`); w.Code < 400 {
			t.Errorf("POST %s in the chat: %d %q", p, w.Code, w.Body)
		}
	}
	for _, p := range []string{"/api/overview", "/api/settings", "/api/runs", "/api/events", "/api/log", "/api/templates", "/api/backups", "/api/download", "/api/stamp"} {
		w := do(h, "GET", s.Host(), p, map[string]string{"Cookie": c["Cookie"]}, "")
		if w.Code == 200 && !strings.Contains(w.Body.String(), "chat") {
			t.Errorf("GET %s in the chat: %d %q", p, w.Code, w.Body)
		}
	}
}
