package chatweb_test

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/chatweb"
	"github.com/pt9912/hetzner-viper/internal/adapter/driving/web"
)

type fakeBackend struct {
	groups  []chatweb.Group
	err     error
	sendErr error
	sent    *[]chatweb.Outgoing // what Send was given
	id      string              // the ID of a question
	done    *func(string, error)
}

func (f fakeBackend) Groups(context.Context) ([]chatweb.Group, error) { return f.groups, f.err }

func (f fakeBackend) Send(_ context.Context, _ string, o chatweb.Outgoing, done func(string, error)) (string, error) {
	if f.sent != nil {
		*f.sent = append(*f.sent, o)
	}
	if f.done != nil {
		*f.done = done
	}
	return f.id, f.sendErr
}

func serve(t *testing.T, b chatweb.Backend, o chatweb.Options) (*web.Server, http.Handler) {
	t.Helper()
	s := web.New(web.Config{Version: o.Version, Readonly: o.Readonly, Chat: chatweb.New(b, o)})
	ln, err := s.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return s, s.Handler()
}

func get(h http.Handler, s *web.Server, cookie, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	r.Host = s.Host()
	if cookie != "" {
		r.Header.Set("Cookie", cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func login(t *testing.T, h http.Handler, s *web.Server) string {
	t.Helper()
	w := get(h, s, "", "/?t="+s.Token())
	if w.Code != 200 {
		t.Fatalf("exchange: %d", w.Code)
	}
	return strings.SplitN(w.Header().Get("Set-Cookie"), ";", 2)[0]
}

func TestTheStateHasTheGroupsTheirMachinesTopicsAndTheOperator(t *testing.T) {
	g := chatweb.Group{Name: "dev", Operator: true, OperatorName: "operator", FileMax: 8 << 20, Topics: []string{"build"},
		Machines: []chatweb.Machine{{Name: "a", Topics: []string{"build"}, PushFromOperator: true}, {Name: "b"}}}
	s, h := serve(t, fakeBackend{groups: []chatweb.Group{g}}, chatweb.Options{Version: "vtest", NoPairs: true})
	c := login(t, h, s)
	w := get(h, s, c, "/api/chat/state")
	body := w.Body.String()
	for _, want := range []string{`"version":"vtest"`, `"no_pairs":true`, `"name":"dev"`, `"operator":true`, `"operator_name":"operator"`, `"file_max":8388608`, `"name":"a"`, `"push_from_operator":true`, `"topics":["build"]`} {
		if w.Code != 200 || !strings.Contains(body, want) {
			t.Errorf("%s is not in %d %s", want, w.Code, body)
		}
	}
	if w := get(h, s, "", "/api/chat/state"); w.Code != http.StatusForbidden {
		t.Errorf("without the cookie: %d", w.Code)
	}
}

func TestAnErrorOfTheBackendIsAnErrorAndNoGroupsIsAnEmptyListNotNull(t *testing.T) {
	s, h := serve(t, fakeBackend{err: errors.New("no settings")}, chatweb.Options{})
	if w := get(h, s, login(t, h, s), "/api/chat/state"); w.Code != 500 || !strings.Contains(w.Body.String(), "no settings") {
		t.Errorf("%d %s", w.Code, w.Body)
	}
	s, h = serve(t, fakeBackend{}, chatweb.Options{})
	if w := get(h, s, login(t, h, s), "/api/chat/state"); w.Code != 200 || !strings.Contains(w.Body.String(), `"groups":[]`) {
		t.Errorf("%d %s", w.Code, w.Body)
	}
}

// The pages: no inline script, no inline style, nothing from elsewhere, and small (docs/plan/ui-chat.md §3.3: an own limit of 175 KB
// at the start; this is the first stage).
func TestThePagesHaveNothingThatTheModelForbidsAndStaySmall(t *testing.T) {
	total := 0
	names := []string{}
	err := fs.WalkDir(chatweb.Pages(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := fs.ReadFile(chatweb.Pages(), p)
		total += len(b)
		names = append(names, p)
		s := string(b)
		if strings.HasSuffix(p, ".html") {
			for _, tag := range strings.Split(s, "<script")[1:] {
				if !strings.HasPrefix(strings.TrimSpace(tag), "src=") {
					t.Errorf("%s: a script without src", p)
				}
			}
			if regexp.MustCompile(`\son[a-z]+=|\sstyle=`).MatchString(s) || strings.Contains(s, "<style") {
				t.Errorf("%s: an inline handler or style", p)
			}
		}
		if regexp.MustCompile(`https?://`).MatchString(s) && !strings.HasSuffix(p, ".svg") {
			t.Errorf("%s: an address of another place", p)
		}
		if strings.HasSuffix(p, ".js") && (strings.Contains(s, "innerHTML") || strings.Contains(s, "eval(") || strings.Contains(s, "document.write")) {
			t.Errorf("%s: markup from text", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(names)
	if want := []string{"chat.js", "favicon.svg", "i18n.de.js", "i18n.en.js", "i18n.js", "index.html", "style.css"}; !slices.Equal(names, want) {
		t.Errorf("the files are %v, not %v", names, want)
	}
	if total > 175<<10 {
		t.Errorf("the pages are %d bytes", total)
	}
}

// Every text is in both languages, and every key that the page asks for is there.
func TestTheTwoLanguagesHaveTheSameKeysAndThePageAsksForNoOther(t *testing.T) {
	read := func(n string) string {
		b, err := fs.ReadFile(chatweb.Pages(), n)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	keys := func(n string) map[string]bool {
		out := map[string]bool{}
		for _, m := range regexp.MustCompile(`'([a-z0-9.]+)':`).FindAllStringSubmatch(read(n), -1) {
			out[m[1]] = true
		}
		return out
	}
	en, de := keys("i18n.en.js"), keys("i18n.de.js")
	if len(en) < 20 {
		t.Fatalf("only %d keys: the test does not read the file", len(en))
	}
	for k := range en {
		if !de[k] {
			t.Errorf("%s is missing in German", k)
		}
	}
	for k := range de {
		if !en[k] {
			t.Errorf("%s is missing in English", k)
		}
	}
	asked := map[string]bool{}
	for _, m := range regexp.MustCompile(`data-i18n(?:-label)?="([a-z0-9.]+)"`).FindAllStringSubmatch(read("index.html"), -1) {
		asked[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`\bt\('([a-z0-9.]+)'`).FindAllStringSubmatch(read("chat.js"), -1) {
		asked[m[1]] = true
	}
	for k := range asked {
		if !en[k] {
			t.Errorf("the page asks for %s, which has no text", k)
		}
	}
}
