package cloudflare

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fake is a Cloudflare zone with a list of A records.
type fake struct {
	mu       sync.Mutex
	records  []Record
	zoneName string
	calls    []string
	bodies   []map[string]any
	fail     map[string]bool // "METHOD path-prefix" -> answer success:false
	srv      *httptest.Server
}

func newFake(t *testing.T, records ...Record) *fake {
	t.Helper()
	f := &fake{records: records, zoneName: "example.com", fail: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) client() *Client { return &Client{Token: "tok-123", ZoneID: "z1", Base: f.srv.URL} }

func (f *fake) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer tok-123" {
		http.Error(w, `{"success":false,"errors":[{"message":"bad token"}]}`, 403)
		return
	}
	key := r.Method + " " + r.URL.Path
	for k := range f.fail {
		if strings.HasPrefix(key, k) {
			io.WriteString(w, `{"success":false,"errors":[{"message":"nope"}],"result":null}`)
			return
		}
	}
	var body map[string]any
	if r.Body != nil {
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			json.Unmarshal(b, &body)
			f.bodies = append(f.bodies, body)
		}
	}
	reply := func(v any) {
		b, _ := json.Marshal(map[string]any{"success": true, "result": v})
		w.Write(b)
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/zones/z1":
		reply(map[string]string{"name": f.zoneName})
	case r.Method == "GET" && r.URL.Path == "/zones/z1/dns_records":
		if r.URL.Query().Get("type") != "A" || r.URL.Query().Get("per_page") != "100" {
			http.Error(w, "bad query", 400)
			return
		}
		var out []Record
		for _, rec := range f.records {
			if rec.Name == r.URL.Query().Get("name") {
				out = append(out, rec)
			}
		}
		if out == nil {
			out = []Record{}
		}
		reply(out)
	case r.Method == "POST" && r.URL.Path == "/zones/z1/dns_records":
		reply(Record{ID: "new"})
	case (r.Method == "PUT" || r.Method == "DELETE") && strings.HasPrefix(r.URL.Path, "/zones/z1/dns_records/"):
		reply(Record{ID: strings.TrimPrefix(r.URL.Path, "/zones/z1/dns_records/")})
	default:
		http.NotFound(w, r)
	}
}

func (f *fake) wrote() bool {
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "GET ") {
			return true
		}
	}
	return false
}

var ctx = context.Background()

func TestSetCreatesTheRecordWithOurComment(t *testing.T) {
	f := newFake(t)
	msg, err := f.client().Set(ctx, "claude-web.example.com", "203.0.113.7")
	if err != nil || msg != "claude-web.example.com -> 203.0.113.7" {
		t.Fatalf("%q %v", msg, err)
	}
	want := map[string]any{"type": "A", "name": "claude-web.example.com", "content": "203.0.113.7", "ttl": float64(60), "proxied": false, "comment": Comment}
	if got := f.bodies[len(f.bodies)-1]; len(got) != len(want) || got["comment"] != Comment || got["ttl"] != float64(60) || got["proxied"] != false || got["content"] != "203.0.113.7" || got["name"] != want["name"] || got["type"] != "A" {
		t.Errorf("body %v", got)
	}
	if f.calls[len(f.calls)-1] != "POST /zones/z1/dns_records" {
		t.Errorf("calls %q", f.calls)
	}
}

func TestSetUpdatesOurRecordAndRefusesForeignOnes(t *testing.T) {
	f := newFake(t, Record{ID: "r1", Name: "h.example.com", Content: "198.51.100.1", Comment: Comment})
	if _, err := f.client().Set(ctx, "h.example.com", "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	if f.calls[len(f.calls)-1] != "PUT /zones/z1/dns_records/r1" {
		t.Errorf("calls %q", f.calls)
	}
	g := newFake(t, Record{ID: "r2", Name: "h.example.com", Content: "198.51.100.1", Comment: "mine"})
	if _, err := g.client().Set(ctx, "h.example.com", "203.0.113.7"); err == nil || err.Error() != "Existing DNS record is not managed by this setup; refusing overwrite" {
		t.Errorf("got %v", err)
	}
	if g.wrote() {
		t.Errorf("a foreign record was written: %q", g.calls)
	}
	h := newFake(t, Record{ID: "a", Name: "h.example.com", Comment: Comment}, Record{ID: "b", Name: "h.example.com", Comment: Comment})
	if _, err := h.client().Set(ctx, "h.example.com", "203.0.113.7"); err == nil || err.Error() != "Multiple A records; refusing to modify" || h.wrote() {
		t.Errorf("got %v, calls %q", err, h.calls)
	}
}

func TestSetRejectsAnythingButAnIPv4Address(t *testing.T) {
	for _, ip := range []string{"", "example.com", "2001:db8::1", "1.2.3", "1.2.3.4; rm", "1.2.3.4\n"} {
		f := newFake(t)
		if _, err := f.client().Set(ctx, "h.example.com", ip); err == nil || f.wrote() {
			t.Errorf("%q: %v, calls %q", ip, err, f.calls)
		}
	}
}

func TestDeleteOnlyRemovesOurRecordAtTheExpectedIP(t *testing.T) {
	ours := Record{ID: "r1", Name: "h.example.com", Content: "203.0.113.7", Comment: Comment}
	f := newFake(t, ours)
	if msg, err := f.client().Delete(ctx, "h.example.com", "203.0.113.7"); err != nil || msg != "DNS record removed (if present)" || f.calls[len(f.calls)-1] != "DELETE /zones/z1/dns_records/r1" {
		t.Errorf("%q %v %q", msg, err, f.calls)
	}
	f = newFake(t, ours)
	if _, err := f.client().Delete(ctx, "h.example.com", ""); err != nil || f.calls[len(f.calls)-1] != "DELETE /zones/z1/dns_records/r1" {
		t.Errorf("without an expected IP: %v %q", err, f.calls)
	}
	f = newFake(t, ours)
	if _, err := f.client().Delete(ctx, "h.example.com", "198.51.100.9"); err == nil || err.Error() != "DNS IP changed; refusing deletion" || f.wrote() {
		t.Errorf("moved record: %v %q", err, f.calls)
	}
	f = newFake(t, Record{ID: "r3", Name: "h.example.com", Content: "1.1.1.1", Comment: "not ours"})
	if _, err := f.client().Delete(ctx, "h.example.com", ""); err == nil || err.Error() != "DNS record not owned by setup; skipping" || f.wrote() {
		t.Errorf("foreign record: %v %q", err, f.calls)
	}
	f = newFake(t)
	if msg, err := f.client().Delete(ctx, "h.example.com", "203.0.113.7"); err != nil || msg != "DNS record removed (if present)" || f.wrote() {
		t.Errorf("no record: %q %v %q", msg, err, f.calls)
	}
}

func TestErrorsAreClearAndNeverContainTheToken(t *testing.T) {
	f := newFake(t)
	c := f.client()
	c.Token = "wrong-token-xyz"
	_, err := c.Set(ctx, "h.example.com", "203.0.113.7")
	if err == nil || !strings.Contains(err.Error(), "Cloudflare lookup of h.example.com failed") || strings.Contains(err.Error(), "wrong-token-xyz") {
		t.Errorf("got %v", err)
	}
	f = newFake(t)
	f.fail["POST "] = true
	if _, err := f.client().Set(ctx, "h.example.com", "203.0.113.7"); err == nil || !strings.HasPrefix(err.Error(), "Cloudflare API error: ") {
		t.Errorf("success:false: %v", err)
	}
	dead := &Client{Token: "tok-123", ZoneID: "z1", Base: "http://127.0.0.1:1"}
	if _, err := dead.Set(ctx, "h.example.com", "203.0.113.7"); err == nil || strings.Contains(err.Error(), "127.0.0.1:1/zones") {
		t.Errorf("unreachable: %v", err)
	}
}

func TestCheckReportsZoneAndRecord(t *testing.T) {
	f := newFake(t)
	lines, ok, err := f.client().Check(ctx, "example.com", "claude-web.example.com")
	if err != nil || !ok || len(lines) != 2 || !strings.HasPrefix(lines[0], "ok    Cloudflare zone example.com readable") || lines[1] != "ok    claude-web.example.com: no record yet" {
		t.Errorf("%q %v %v", lines, ok, err)
	}
	f = newFake(t, Record{ID: "r", Name: "claude-web.example.com", Content: "203.0.113.7", Comment: Comment})
	lines, ok, _ = f.client().Check(ctx, "sub.example.com", "claude-web.example.com")
	if !ok || lines[1] != "info  claude-web.example.com -> 203.0.113.7 (managed by this setup)" {
		t.Errorf("ours: %q %v", lines, ok)
	}
	f = newFake(t, Record{ID: "r", Name: "claude-web.example.com", Content: "1.1.1.1", Comment: "other"})
	lines, ok, _ = f.client().Check(ctx, "example.com", "claude-web.example.com")
	if ok || lines[len(lines)-1] != "FAIL  claude-web.example.com already has a record not managed by this setup" {
		t.Errorf("foreign: %q %v", lines, ok)
	}
	f = newFake(t)
	lines, ok, _ = f.client().Check(ctx, "other.org", "h.other.org")
	if ok || lines[0] != "FAIL  DNS_DOMAIN other.org is not in Cloudflare zone example.com" {
		t.Errorf("wrong zone: %q %v", lines, ok)
	}
	f = newFake(t)
	f.fail["GET /zones/z1"] = false
	f.zoneName = "example.com"
	c := f.client()
	c.ZoneID = "z9"
	if _, _, err := c.Check(ctx, "example.com", "h.example.com"); err == nil {
		t.Error("an unreadable zone fails the lookup first, like dns.sh")
	}
	for _, d := range []string{"notexample.com", "example.com.evil.org"} {
		g := newFake(t)
		if _, ok, _ := g.client().Check(ctx, d, "h."+d); ok {
			t.Errorf("%s must not match the zone example.com", d)
		}
	}
}
