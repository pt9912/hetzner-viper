package hetzner_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/hetzner"
)

// A minimal Hetzner API: enough JSON for the calls the client makes.
func api(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "unauthorized", "message": "unable to authenticate"}})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		page := map[string]any{"meta": map[string]any{"pagination": map[string]any{"page": 1, "per_page": 50, "last_page": 1, "total_entries": 1}}}
		switch r.URL.Path {
		case "/servers":
			page["servers"] = []any{map[string]any{"id": 1, "name": "claude-web"}, map[string]any{"id": 2, "name": "claude-api"}}
		case "/server_types":
			if r.URL.Query().Get("name") != "cpx32" {
				page["server_types"] = []any{}
				break
			}
			page["server_types"] = []any{map[string]any{
				"id": 105, "name": "cpx32", "cores": 4, "memory": 8, "disk": 160, "architecture": "x86",
				"locations": []any{
					map[string]any{"id": 1, "name": "fsn1", "available": true, "recommended": true, "deprecation": nil},
					map[string]any{"id": 2, "name": "nbg1", "available": false, "deprecation": map[string]any{"announced": "2026-01-01T00:00:00Z", "unavailable_after": "2026-12-31T00:00:00Z"}},
				},
			}}
		case "/locations":
			if r.URL.Query().Get("name") == "fsn1" {
				page["locations"] = []any{map[string]any{"id": 1, "name": "fsn1"}}
			} else {
				page["locations"] = []any{}
			}
		case "/images":
			if r.URL.Query().Get("name") == "ubuntu-26.04" && r.URL.Query().Get("architecture") == "x86" {
				page["images"] = []any{map[string]any{"id": 9, "name": "ubuntu-26.04", "architecture": "x86", "type": "system"}}
			} else {
				page["images"] = []any{}
			}
		case "/ssh_keys":
			page["ssh_keys"] = []any{map[string]any{"id": 5, "name": "claude-login", "fingerprint": "aa:bb:cc"}}
		default:
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestReadsTheProject(t *testing.T) {
	srv := api(t)
	h := hetzner.New("good", srv.URL)
	names, err := h.ServerNames(context.Background())
	if err != nil || strings.Join(names, ",") != "claude-web,claude-api" {
		t.Errorf("%q %v", names, err)
	}
	st, err := h.ServerType(context.Background(), "cpx32")
	if err != nil || st == nil || st.Cores != 4 || st.MemoryGB != 8 || st.DiskGB != 160 || st.Architecture != "x86" {
		t.Fatalf("%+v %v", st, err)
	}
	if len(st.Locations) != 2 || !st.Locations[0].Available || st.Locations[0].Name != "fsn1" || !st.Locations[0].UnavailableAfter.IsZero() {
		t.Errorf("fsn1: %+v", st.Locations)
	}
	if st.Locations[1].Available || !st.Locations[1].UnavailableAfter.Equal(time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("nbg1: %+v", st.Locations[1])
	}
	if st, err := h.ServerType(context.Background(), "nope"); err != nil || st != nil {
		t.Errorf("unknown type: %v %v", st, err)
	}
	if ok, err := h.LocationExists(context.Background(), "fsn1"); !ok || err != nil {
		t.Errorf("fsn1 %v %v", ok, err)
	}
	if ok, _ := h.LocationExists(context.Background(), "mars1"); ok {
		t.Error("mars1")
	}
	if ok, err := h.ImageExists(context.Background(), "ubuntu-26.04", "x86"); !ok || err != nil {
		t.Errorf("image %v %v", ok, err)
	}
	if ok, _ := h.ImageExists(context.Background(), "ubuntu-26.04", "arm"); ok {
		t.Error("no arm image")
	}
	keys, err := h.SSHKeys(context.Background())
	if err != nil || len(keys) != 1 || keys[0].Name != "claude-login" || keys[0].Fingerprint != "aa:bb:cc" {
		t.Errorf("%+v %v", keys, err)
	}
}

func TestABadTokenIsAnErrorWithoutTheToken(t *testing.T) {
	srv := api(t)
	_, err := hetzner.New("secret-bad-token", srv.URL).ServerNames(context.Background())
	if err == nil || strings.Contains(hetzner.Error(err), "secret-bad-token") || !strings.Contains(hetzner.Error(err), "HCLOUD_TOKEN") {
		t.Errorf("got %v / %q", err, hetzner.Error(err))
	}
	if _, err := hetzner.New("good", "http://127.0.0.1:1").ServerNames(context.Background()); err == nil {
		t.Error("an unreachable API is an error")
	}
}
