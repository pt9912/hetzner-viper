package web_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/web"
)

func (st *site) upload(t *testing.T, query, body, ctype, origin string) (int, string) {
	t.Helper()
	if !strings.Contains(query, "group=") {
		query += "&group=dev"
	}
	req, _ := http.NewRequest("POST", st.base+"/api/upload?"+query, strings.NewReader(body))
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("X-Viper-UI", "1")
	req.Header.Set("Origin", origin)
	resp, err := st.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func uploadID(t *testing.T, body string) string {
	t.Helper()
	var r struct{ ID string }
	if err := jsonUnmarshal(body, &r); err != nil || r.ID == "" {
		t.Fatalf("upload answer %q", body)
	}
	return r.ID
}

func TestPeersAndTopicsAreRunsOfViperNats(t *testing.T) {
	st := live(t, false)
	_, code := st.start(t, `{"action":"nats","sub":"peers","nats":"dev","timeout":5,"from":"dev2"}`)
	if code != 200 {
		t.Fatal(code)
	}
	p := st.spawn.next(t)
	if got := strings.Join(p.argv, " "); got != "nats peers dev --from=dev2 --timeout=5 --json" {
		t.Errorf("argv %q", got)
	}
	p.end(0)
	_, code = st.start(t, `{"action":"nats","sub":"listen","nats":"dev","topic":"build,news","all":true}`)
	if code != 200 {
		t.Fatal(code)
	}
	l := st.spawn.next(t)
	if got := strings.Join(l.argv, " "); got != "nats listen dev --topic=build,news --all --json" {
		t.Errorf("argv %q", got)
	}
	l.end(130)
}

func TestAMessageGoesByFilesOfTheRunNotByTheCommandLine(t *testing.T) {
	st := live(t, false)
	code, body := st.upload(t, "kind=pdf&name=../../report.pdf", "%PDF-1.4 data", "application/octet-stream", st.base)
	if code != 200 {
		t.Fatal(code, body)
	}
	up := uploadID(t, body)
	_, c := st.start(t, `{"action":"nats","sub":"send","nats":"dev","to":"dev1","text":"look at \"this\"; $(rm -rf x)","upload":"`+up+`","yes":true}`)
	if c != 200 {
		t.Fatal(c)
	}
	p := st.spawn.next(t)
	got := strings.Join(p.argv, " ")
	if strings.Contains(got, "rm -rf") || strings.Contains(got, "look at") {
		t.Errorf("the text is in the command line: %s", got)
	}
	var textFile, pdf string
	for _, a := range p.argv {
		if v, ok := strings.CutPrefix(a, "--file="); ok {
			textFile = v
		}
		if v, ok := strings.CutPrefix(a, "--pdf="); ok {
			pdf = v
		}
	}
	if b, err := os.ReadFile(textFile); err != nil || string(b) != `look at "this"; $(rm -rf x)` {
		t.Errorf("text file: %q %v", b, err)
	}
	if filepath.Base(pdf) != "report.pdf" {
		t.Errorf("name of the file: %s", pdf)
	}
	if b, err := os.ReadFile(pdf); err != nil || string(b) != "%PDF-1.4 data" {
		t.Errorf("file: %q %v", b, err)
	}
	if runtimeUnix() {
		for _, f := range []string{textFile, pdf} {
			if fi, _ := os.Stat(f); fi == nil || fi.Mode().Perm() != 0o600 {
				t.Errorf("%s: %v", f, fi)
			}
		}
	}
	for _, want := range []string{"nats send dev ", "--to=dev1", "--yes", "--json"} {
		if !strings.Contains(got, want) {
			t.Errorf("argv lacks %q: %s", want, got)
		}
	}
	p.end(0)
	deadline := time.Now().Add(5 * time.Second)
	for exists(textFile) || exists(pdf) {
		if time.Now().After(deadline) {
			t.Fatal("the files of the run stayed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// the uploaded file is used up
	if _, c := st.start(t, `{"action":"nats","sub":"send","nats":"dev","to":"dev1","upload":"`+up+`"}`); c != http.StatusBadRequest {
		t.Errorf("a file used twice: %d", c)
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestNATSRequestsAreChecked(t *testing.T) {
	st := live(t, false)
	long := strings.Repeat("x", 16<<10+1)
	for name, body := range map[string]string{
		"group":          `{"action":"nats","sub":"peers","nats":"nope"}`,
		"no group":       `{"action":"nats","sub":"peers"}`,
		"subcommand":     `{"action":"nats","sub":"init","nats":"dev"}`,
		"revoke":         `{"action":"nats","sub":"revoke","nats":"dev"}`,
		"to":             `{"action":"nats","sub":"send","nats":"dev","to":"web","text":"x"}`,
		"from":           `{"action":"nats","sub":"peers","nats":"dev","from":"--help"}`,
		"no to":          `{"action":"nats","sub":"send","nats":"dev","text":"x"}`,
		"topic dash":     `{"action":"nats","sub":"listen","nats":"dev","topic":"-x"}`,
		"topic space":    `{"action":"nats","sub":"listen","nats":"dev","topic":"a b"}`,
		"topic missing":  `{"action":"nats","sub":"broadcast","nats":"dev","text":"x"}`,
		"timeout":        `{"action":"nats","sub":"peers","nats":"dev","timeout":700}`,
		"negative":       `{"action":"nats","sub":"peers","nats":"dev","timeout":-1}`,
		"empty message":  `{"action":"nats","sub":"send","nats":"dev","to":"dev1"}`,
		"long text":      `{"action":"nats","sub":"send","nats":"dev","to":"dev1","text":"` + long + `"}`,
		"no such upload": `{"action":"nats","sub":"send","nats":"dev","to":"dev1","upload":"zzz"}`,
		"fetch sha":      `{"action":"nats","sub":"fetch","nats":"dev","id":"abc","sha256":"zz"}`,
		"fetch id":       `{"action":"nats","sub":"fetch","nats":"dev","id":"--out=/etc","sha256":"` + strings.Repeat("a", 64) + `"}`,
		"fetch no id":    `{"action":"nats","sub":"fetch","nats":"dev","sha256":"` + strings.Repeat("a", 64) + `"}`,
	} {
		if _, c := st.start(t, body); c != http.StatusBadRequest {
			t.Errorf("%s: %d", name, c)
		}
	}
	select {
	case p := <-st.spawn.ch:
		t.Errorf("a process was started: %v", p.argv)
	default:
	}
}

func TestUploadsAreGuarded(t *testing.T) {
	st := liveWith(t, false, func(c *web.Config) { c.MaxUpload = 1024 })
	if code, _ := st.upload(t, "kind=pdf", strings.Repeat("x", 2000), "application/octet-stream", st.base); code != http.StatusRequestEntityTooLarge {
		t.Errorf("too big: %d", code)
	}
	if code, _ := st.upload(t, "kind=exe", "x", "application/octet-stream", st.base); code != http.StatusBadRequest {
		t.Errorf("kind: %d", code)
	}
	if code, _ := st.upload(t, "kind=pdf", "x", "application/octet-stream", "http://evil.example"); code != http.StatusForbidden {
		t.Errorf("origin: %d", code)
	}
	if code, _ := st.upload(t, "kind=pdf", "x", "application/json", st.base); code != http.StatusForbidden {
		t.Errorf("json type: %d", code)
	}
	if code, _ := st.upload(t, "kind=pdf", "x", "multipart/form-data; boundary=x", st.base); code != http.StatusForbidden {
		t.Errorf("form type: %d", code)
	}
	for i := 0; i < 8; i++ {
		if code, b := st.upload(t, "kind=zip&name=a.zip", "x", "application/octet-stream", st.base); code != 200 {
			t.Fatalf("upload %d: %d %s", i, code, b)
		}
	}
	if code, _ := st.upload(t, "kind=zip", "x", "application/octet-stream", st.base); code != http.StatusServiceUnavailable {
		t.Errorf("a ninth file: %d", code)
	}
	ro := live(t, true)
	if code, _ := ro.upload(t, "kind=pdf", "x", "application/octet-stream", ro.base); code != http.StatusForbidden {
		t.Errorf("read only: %d", code)
	}
}

func TestAFetchedFileIsOfferedAsADownloadOnly(t *testing.T) {
	st := live(t, false)
	sha := strings.Repeat("ab", 32)
	id, c := st.start(t, `{"action":"nats","sub":"fetch","nats":"dev","id":"obj_1","sha256":"`+sha+`"}`)
	if c != 200 {
		t.Fatal(c)
	}
	p := st.spawn.next(t)
	var out string
	for _, a := range p.argv {
		if v, ok := strings.CutPrefix(a, "--out="); ok {
			out = v
		}
	}
	if p.argv[len(p.argv)-2] != "obj_1" || p.argv[len(p.argv)-1] != "--json" || out == "" {
		t.Fatalf("argv %v", p.argv)
	}
	get := func() (int, http.Header, string) {
		resp, err := st.c.Get(st.base + "/api/download?id=" + id)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header, string(b)
	}
	if code, _, _ := get(); code != http.StatusNotFound {
		t.Errorf("while it runs: %d", code)
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "plan.pdf"), []byte("%PDF fetched"), 0o600); err != nil {
		t.Fatal(err)
	}
	p.end(0)
	s := st.attach(t, id)
	s.until(t, state("done"))
	code, h, body := get()
	if code != 200 || body != "%PDF fetched" || h.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment; filename*=UTF-8''plan.pdf") || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("download: %d %v %q", code, h, body)
	}
	if code, _, _ := (func() (int, http.Header, string) {
		resp, err := st.c.Get(st.base + "/api/download?id=nope")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode, nil, ""
	})(); code != http.StatusNotFound {
		t.Errorf("unknown run: %d", code)
	}
}

func TestOnlyAFetchWithOneFileIsDownloaded(t *testing.T) {
	st := live(t, false)
	// a send run has nothing to take
	id, _ := st.start(t, `{"action":"nats","sub":"send","nats":"dev","to":"dev1","text":"x"}`)
	p := st.spawn.next(t)
	p.end(0)
	st.attach(t, id).until(t, state("done"))
	resp, _ := st.c.Get(st.base + "/api/download?id=" + id)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("send: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
	// a failed fetch, and one with two files
	for name, files := range map[string][]string{"failed": {"a"}, "two": {"a", "b"}} {
		fid, _ := st.start(t, `{"action":"nats","sub":"fetch","nats":"dev","id":"o","sha256":"`+strings.Repeat("c", 64)+`"}`)
		fp := st.spawn.next(t)
		var out string
		for _, a := range fp.argv {
			if v, ok := strings.CutPrefix(a, "--out="); ok {
				out = v
			}
		}
		_ = os.MkdirAll(out, 0o700)
		for _, f := range files {
			_ = os.WriteFile(filepath.Join(out, f), []byte("x"), 0o600)
		}
		code := 0
		if name == "failed" {
			code = 1
		}
		fp.end(code)
		st.attach(t, fid).until(t, state("done"))
		r, _ := st.c.Get(st.base + "/api/download?id=" + fid)
		if r.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d", name, r.StatusCode)
		}
		_ = r.Body.Close()
	}
}

var _ = bytes.NewReader

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }

func runtimeUnix() bool { return runtime.GOOS != "windows" }
