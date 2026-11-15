package web_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/web"
)

type fakeEditor struct {
	mu      sync.Mutex
	made    []web.EditRequest
	enter   chan struct{}
	release chan struct{}
}

func (e *fakeEditor) Templates(context.Context) (web.Templates, error) {
	return web.Templates{Providers: []web.Template{{Name: "hetzner", Type: "cpx32", Location: "fsn1"}}, Machines: []string{"web"}, Base: true}, nil
}

func (e *fakeEditor) Backups(context.Context) ([]web.BackupInfo, error) {
	return []web.BackupInfo{{Name: "20260101T000000-setting.zip", Time: "2026-01-01T00:00:00Z", Files: []string{".env.local"}}}, nil
}

func (e *fakeEditor) OpenBackup(_ context.Context, name string) (io.ReadCloser, error) {
	if name != "20260101T000000-setting.zip" {
		return nil, errors.New("no such backup")
	}
	return io.NopCloser(strings.NewReader("PK-fake")), nil
}

func (e *fakeEditor) Duplicate(_ context.Context, kind, name string) (web.Copy, error) {
	if name != "web" {
		return web.Copy{}, errors.New("no such machine")
	}
	return web.Copy{Kind: kind, Name: "web-2", Fields: map[string]string{"SERVER_TYPE": "cpx22 s3cr3t-value-123"}, Skipped: []string{"REPO_URL"}}, nil
}

func (e *fakeEditor) Preview(_ context.Context, r web.EditRequest) (web.EditResult, error) {
	res := web.EditResult{Files: []web.EditFile{{Path: "machines/" + r.Name + ".env.local", Text: "A='s3cr3t-value-123'\n"}}}
	if r.Name == "bad" {
		res.Problems = []string{"name: no"}
	}
	return res, nil
}

func (e *fakeEditor) Create(ctx context.Context, r web.EditRequest) (web.EditResult, error) {
	if e.enter != nil {
		e.enter <- struct{}{}
		<-e.release
	}
	res, _ := e.Preview(ctx, r)
	if len(res.Problems) > 0 {
		return res, errors.New("the change is refused")
	}
	e.mu.Lock()
	e.made = append(e.made, r)
	e.mu.Unlock()
	return res, nil
}

func editServer(t *testing.T, ed web.Editor, readonly bool) (*web.Server, http.Handler, map[string]string) {
	t.Helper()
	s := web.New(web.Config{Backend: newFake(), Editor: ed, Readonly: readonly, Version: "vtest", Redact: redact})
	ln, err := s.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	h := s.Handler()
	return s, h, login(t, s, h)
}

func edit(s *web.Server, h http.Handler, c map[string]string, path, body string) (int, string) {
	w := do(h, "POST", s.Host(), path, map[string]string{"Cookie": c["Cookie"], "Content-Type": "application/json", "X-Viper-UI": "1", "Origin": "http://" + s.Host()}, body)
	return w.Code, w.Body.String()
}

func TestWithoutEditNothingIsWrittenAndThePageSaysSo(t *testing.T) {
	for _, readonly := range []bool{false, true} {
		var ed web.Editor
		if readonly {
			ed = &fakeEditor{}
		}
		s, h, c := editServer(t, ed, readonly)
		for _, p := range []string{"/api/edit/preview", "/api/edit/apply"} {
			if code, _ := edit(s, h, c, p, `{"kind":"machine","name":"x"}`); code != http.StatusForbidden {
				t.Errorf("%s (readonly %v): %d", p, readonly, code)
			}
		}
		if w := do(h, "GET", s.Host(), "/api/templates", c, ""); w.Code != http.StatusForbidden {
			t.Errorf("templates: %d", w.Code)
		}
		if w := do(h, "GET", s.Host(), "/api/overview", c, ""); strings.Contains(w.Body.String(), `"edit":true`) || !strings.Contains(w.Body.String(), `"edit":false`) {
			t.Errorf("overview: %s", w.Body)
		}
	}
}

func TestPreviewAndApplyWithEdit(t *testing.T) {
	ed := &fakeEditor{}
	s, h, c := editServer(t, ed, false)
	if w := do(h, "GET", s.Host(), "/api/overview", c, ""); !strings.Contains(w.Body.String(), `"edit":true`) {
		t.Errorf("overview: %s", w.Body)
	}
	if w := do(h, "GET", s.Host(), "/api/templates", c, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"cpx32"`) {
		t.Errorf("templates: %d %s", w.Code, w.Body)
	}
	code, body := edit(s, h, c, "/api/edit/preview", `{"kind":"machine","name":"web2","fields":{"PROVIDER":"hetzner"},"members":null}`)
	if code != 200 || !strings.Contains(body, "machines/web2.env.local") || strings.Contains(body, "s3cr3t-value-123") {
		t.Errorf("preview: %d %s (the text goes through the redaction)", code, body)
	}
	if len(ed.made) != 0 {
		t.Error("the preview made a file")
	}
	code, body = edit(s, h, c, "/api/edit/apply", `{"kind":"machine","name":"web2","fields":{},"members":[]}`)
	if code != 200 || !strings.Contains(body, `"done":true`) || len(ed.made) != 1 {
		t.Errorf("apply: %d %s", code, body)
	}
	code, body = edit(s, h, c, "/api/edit/apply", `{"kind":"machine","name":"bad","fields":{},"members":[]}`)
	if code != 200 || !strings.Contains(body, "name: no") || strings.Contains(body, `"done":true`) || !strings.Contains(body, `"done":false`) {
		t.Errorf("refused: %d %s", code, body)
	}
}

func TestABadEditRequestAndTheGuard(t *testing.T) {
	s, h, c := editServer(t, &fakeEditor{}, false)
	for _, body := range []string{`{`, `{"kind":"machine","path":"../x"}`, `{"kind":"machine","members":["` + strings.Repeat(`a","`, 300) + `a"]}`} {
		if code, _ := edit(s, h, c, "/api/edit/preview", body); code != http.StatusBadRequest {
			t.Errorf("%.30s: %d", body, code)
		}
	}
	hdr := map[string]string{"Cookie": c["Cookie"], "Content-Type": "text/plain", "X-Viper-UI": "1", "Origin": "http://" + s.Host()}
	if w := do(h, "POST", s.Host(), "/api/edit/apply", hdr, `{"kind":"machine","name":"x"}`); w.Code != http.StatusForbidden {
		t.Errorf("a form post: %d", w.Code)
	}
	if w := do(h, "POST", s.Host(), "/api/edit/apply", map[string]string{"Content-Type": "application/json", "X-Viper-UI": "1", "Origin": "http://" + s.Host()}, `{}`); w.Code != http.StatusForbidden {
		t.Errorf("without the cookie: %d", w.Code)
	}
}

func TestOneChangeAtATime(t *testing.T) {
	ed := &fakeEditor{enter: make(chan struct{}), release: make(chan struct{})}
	s, h, c := editServer(t, ed, false)
	first := make(chan int, 1)
	go func() { code, _ := edit(s, h, c, "/api/edit/apply", `{"kind":"machine","name":"a"}`); first <- code }()
	<-ed.enter
	if code, _ := edit(s, h, c, "/api/edit/apply", `{"kind":"machine","name":"b"}`); code != http.StatusConflict {
		t.Errorf("second: %d", code)
	}
	close(ed.release)
	if code := <-first; code != 200 {
		t.Errorf("first: %d", code)
	}
}

func TestNoCredentialIsChangedUnderARun(t *testing.T) {
	ed := &fakeEditor{}
	st := liveWith(t, false, func(c *web.Config) { c.Editor = ed })
	id, code := st.start(t, `{"action":"preflight","machine":"web"}`)
	if code != 200 || id == "" {
		t.Fatal(code)
	}
	p := st.spawn.next(t)
	body := `{"kind":"credential","target":"base","name":"","fields":{"HCLOUD_TOKEN":"abcdefgh12345"},"remove":[],"etag":"x"}`
	if code, b := st.post(t, "/api/edit/apply", body); code != http.StatusConflict || strings.Contains(b, "abcdefgh12345") {
		t.Errorf("under a run: %d %s", code, b)
	}
	if len(ed.made) != 0 {
		t.Error("changed under a run")
	}
	// a new file is not a credential: it may be made
	if code, _ := st.post(t, "/api/edit/apply", `{"kind":"machine","name":"web2","fields":{},"members":[]}`); code != 200 {
		t.Errorf("a new file under a run: %d", code)
	}
	p.end(0)
	st.attach(t, id).until(t, state("done"))
	if code, _ := st.post(t, "/api/edit/apply", body); code != 200 {
		t.Errorf("after the run: %d", code)
	}
}

func TestACredentialRequestIsRefusedAsAFormAndWithoutEdit(t *testing.T) {
	st := live(t, false)
	body := `{"kind":"credential","target":"base","fields":{"HCLOUD_TOKEN":"abcdefgh12345"}}`
	if code, b := st.post(t, "/api/edit/preview", body); code != http.StatusForbidden || strings.Contains(b, "abcdefgh12345") {
		t.Errorf("%d %s", code, b)
	}
}

func TestNoRunStartsWhileACredentialIsWritten(t *testing.T) {
	ed := &fakeEditor{enter: make(chan struct{}), release: make(chan struct{})}
	st := liveWith(t, false, func(c *web.Config) { c.Editor = ed })
	done := make(chan int, 1)
	go func() {
		code, _ := st.post(t, "/api/edit/apply", `{"kind":"credential","target":"base","fields":{"HCLOUD_TOKEN":"abcdefgh12345"},"etag":"x"}`)
		done <- code
	}()
	<-ed.enter
	if _, code := st.start(t, `{"action":"preflight","machine":"web"}`); code != http.StatusConflict {
		t.Errorf("a run started while the credential is written: %d", code)
	}
	close(ed.release)
	if code := <-done; code != 200 {
		t.Errorf("apply: %d", code)
	}
	if _, code := st.start(t, `{"action":"preflight","machine":"web"}`); code != 200 {
		t.Errorf("after the change: %d", code)
	}
}

func TestADuplicateIsOnlyOfferedWithEdit(t *testing.T) {
	s, h, c := editServer(t, &fakeEditor{}, false)
	w := do(h, "GET", s.Host(), "/api/duplicate?kind=machine&name=web", c, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"web-2"`) || strings.Contains(w.Body.String(), "s3cr3t-value-123") {
		t.Errorf("%d %s", w.Code, w.Body)
	}
	if w := do(h, "GET", s.Host(), "/api/duplicate?kind=machine&name=nope", c, ""); w.Code != http.StatusNotFound {
		t.Errorf("%d", w.Code)
	}
	sr, hr, cr := editServer(t, &fakeEditor{}, true) // --readonly with an editor: refused as well
	if w := do(hr, "GET", sr.Host(), "/api/duplicate?kind=machine&name=web", cr, ""); w.Code != http.StatusForbidden {
		t.Errorf("readonly: %d", w.Code)
	}
	s2, h2, c2 := editServer(t, nil, false)
	if w := do(h2, "GET", s2.Host(), "/api/duplicate?kind=machine&name=web", c2, ""); w.Code != http.StatusForbidden {
		t.Errorf("without edit: %d", w.Code)
	}
}

func TestBackupsAreListedAndDownloadedOnlyWithEdit(t *testing.T) {
	s, h, c := editServer(t, &fakeEditor{}, false)
	if w := do(h, "GET", s.Host(), "/api/backups", c, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "20260101T000000-setting.zip") {
		t.Errorf("%d %s", w.Code, w.Body)
	}
	w := do(h, "GET", s.Host(), "/api/backups/20260101T000000-setting.zip", c, "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/octet-stream" || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("%d %v", w.Code, w.Header())
	}
	if w := do(h, "GET", s.Host(), "/api/backups/nope.zip", c, ""); w.Code != http.StatusNotFound {
		t.Errorf("%d", w.Code)
	}
	s2, h2, c2 := editServer(t, nil, false)
	for _, p := range []string{"/api/backups", "/api/backups/20260101T000000-setting.zip"} {
		if w := do(h2, "GET", s2.Host(), p, c2, ""); w.Code != http.StatusForbidden {
			t.Errorf("%s without edit: %d", p, w.Code)
		}
	}
}
