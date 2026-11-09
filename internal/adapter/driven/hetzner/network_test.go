package hetzner_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/hetzner"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/cloud"
)

func nets(t *testing.T, cidrs ...string) []*net.IPNet {
	t.Helper()
	var out []*net.IPNet
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func TestPickRangeAvoidsExistingNetworks(t *testing.T) {
	n1, s1, err := hetzner.PickRange(nil, "dev")
	if err != nil || !strings.HasPrefix(n1, "10.1") || !strings.HasSuffix(n1, ".0.0/16") || !strings.HasSuffix(s1, ".0.0/24") {
		t.Fatalf("%s %s %v", n1, s1, err)
	}
	// the same group gets the same place to start; a taken range moves it on
	n2, _, _ := hetzner.PickRange(nets(t, n1), "dev")
	if n2 == n1 {
		t.Errorf("%s is taken", n1)
	}
	// a small existing network inside the candidate counts as a clash, and so does a bigger one around it
	_, inner, _ := net.ParseCIDR(strings.Replace(n1, "0.0/16", "5.0/24", 1))
	if n3, _, _ := hetzner.PickRange([]*net.IPNet{inner}, "dev"); n3 == n1 {
		t.Errorf("%s overlaps %s", n1, inner)
	}
	if n4, _, _ := hetzner.PickRange(nets(t, "10.0.0.0/8"), "dev"); n4 != "" {
		t.Errorf("10.0.0.0/8 covers every candidate, got %s", n4)
	}
	_, _, err = hetzner.PickRange(nets(t, "10.0.0.0/8"), "dev")
	if err == nil {
		t.Error("no range left must be an error")
	}
}

// netAPI is a Hetzner project with networks and servers; names of networks are unique.
type netAPI struct {
	mu       sync.Mutex
	networks map[int]map[string]any
	nextID   int
	servers  map[int][]any // server id -> private_net entries
	creates  int
}

func newNetAPI(t *testing.T) (*netAPI, *hetzner.Client) {
	t.Helper()
	a := &netAPI{networks: map[int]map[string]any{}, nextID: 100, servers: map[int][]any{5: {}}}
	srv := httptest.NewServer(http.HandlerFunc(a.handle))
	t.Cleanup(srv.Close)
	return a, hetzner.New("good", srv.URL)
}

func (a *netAPI) serverJSON(id int) map[string]any {
	s := server(id, "claude-x", map[string]string{}, "")
	s["private_net"] = a.servers[id]
	return s
}

func (a *netAPI) handle(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	reply := func(v map[string]any) { _ = json.NewEncoder(w).Encode(v) }
	fail := func(status int, code, msg string) {
		w.WriteHeader(status)
		reply(map[string]any{"error": map[string]any{"code": code, "message": msg}})
	}
	page := func(key string, list []any) {
		reply(map[string]any{key: list, "meta": map[string]any{"pagination": map[string]any{"page": 1, "per_page": 50, "last_page": 1, "total_entries": len(list)}}})
	}
	path := r.URL.Path
	switch {
	case r.Method == "GET" && path == "/locations":
		z := map[string]string{"fsn1": "eu-central", "ash": "us-east"}[r.URL.Query().Get("name")]
		if z == "" {
			page("locations", []any{})
		} else {
			page("locations", []any{map[string]any{"id": 1, "name": r.URL.Query().Get("name"), "network_zone": z}})
		}
	case r.Method == "GET" && path == "/networks":
		var list []any
		for _, n := range a.networks {
			if q := r.URL.Query().Get("name"); q == "" || q == n["name"] {
				list = append(list, n)
			}
		}
		page("networks", list)
	case r.Method == "POST" && path == "/networks":
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		for _, n := range a.networks {
			if n["name"] == body["name"] {
				fail(409, "uniqueness_error", "name is already used")
				return
			}
		}
		a.creates++
		a.nextID++
		subs := []any{}
		for _, s := range body["subnets"].([]any) {
			sm := s.(map[string]any)
			subs = append(subs, map[string]any{"type": "cloud", "ip_range": sm["ip_range"], "network_zone": sm["network_zone"], "gateway": "10.0.0.1"})
		}
		n := map[string]any{"id": a.nextID, "name": body["name"], "ip_range": body["ip_range"], "subnets": subs, "routes": []any{}, "servers": []any{}, "labels": body["labels"], "created": "2026-10-23T10:00:00Z", "protection": map[string]any{"delete": false}, "expose_routes_to_vswitch": false}
		a.networks[a.nextID] = n
		reply(map[string]any{"network": n})
	case strings.HasPrefix(path, "/networks/"):
		id, _ := strconv.Atoi(strings.TrimPrefix(path, "/networks/"))
		n, ok := a.networks[id]
		if !ok {
			fail(404, "not_found", "network not found")
			return
		}
		if r.Method == "DELETE" {
			delete(a.networks, id)
			reply(map[string]any{})
			return
		}
		reply(map[string]any{"network": n})
	case r.Method == "GET" && strings.HasPrefix(path, "/servers/"):
		id, _ := strconv.Atoi(strings.TrimPrefix(path, "/servers/"))
		if _, ok := a.servers[id]; !ok {
			fail(404, "not_found", "server not found")
			return
		}
		reply(map[string]any{"server": a.serverJSON(id)})
	case r.Method == "POST" && strings.HasSuffix(path, "/actions/attach_to_network"):
		id, _ := strconv.Atoi(strings.Split(path, "/")[2])
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		a.servers[id] = append(a.servers[id], map[string]any{"network": body["network"], "ip": "10.123.0.2", "alias_ips": []any{}, "mac_address": "86:00:00:00:00:01"})
		reply(map[string]any{"action": map[string]any{"id": 7, "status": "success", "progress": 100, "command": "attach_to_network", "started": "2026-01-01T00:00:00Z", "finished": "2026-01-01T00:00:01Z"}})
	case strings.HasPrefix(path, "/actions/"):
		reply(map[string]any{"action": map[string]any{"id": 7, "status": "success", "progress": 100, "command": "x", "started": "2026-01-01T00:00:00Z", "finished": "2026-01-01T00:00:01Z"}})
	default:
		http.NotFound(w, r)
	}
}

func TestEnsurePrivateNetworkCreatesOnceAndFindsAfterwards(t *testing.T) {
	a, c := newNetAPI(t)
	ctx := context.Background()
	n, made, err := c.EnsurePrivateNetwork(ctx, "dev", "fsn1")
	if err != nil || !made || n.Name != "viper-nats-dev" || n.Zone != "eu-central" || n.ID == "" || !strings.HasSuffix(n.IPRange, ".0.0/16") {
		t.Fatalf("%+v %v %v", n, made, err)
	}
	n2, made, err := c.EnsurePrivateNetwork(ctx, "dev", "fsn1")
	if err != nil || made || n2.ID != n.ID {
		t.Fatalf("second caller: %+v %v %v", n2, made, err)
	}
	if a.creates != 1 {
		t.Errorf("%d networks created", a.creates)
	}
	for _, x := range a.networks {
		l := x["labels"].(map[string]any)
		if l[model.LabelManaged] != "true" || l[model.LabelNATSGroup] != "dev" {
			t.Errorf("labels %v", l)
		}
	}
	if f, err := c.FindPrivateNetwork(ctx, "dev"); err != nil || f == nil || f.ID != n.ID {
		t.Errorf("find: %+v %v", f, err)
	}
	if f, err := c.FindPrivateNetwork(ctx, "other"); err != nil || f != nil {
		t.Errorf("no such group: %+v %v", f, err)
	}
}

func TestEnsurePrivateNetworkRefusesAWrongZoneAndForeignNetworks(t *testing.T) {
	a, c := newNetAPI(t)
	ctx := context.Background()
	if _, _, err := c.EnsurePrivateNetwork(ctx, "dev", "fsn1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.EnsurePrivateNetwork(ctx, "dev", "ash"); err == nil || !strings.Contains(err.Error(), "one network zone") {
		t.Errorf("ash: %v", err)
	}
	if _, _, err := c.EnsurePrivateNetwork(ctx, "dev", "nowhere"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown location: %v", err)
	}
	// a network of that name that viper did not create
	a.networks[1] = map[string]any{"id": 1, "name": "viper-nats-mine", "ip_range": "10.9.0.0/16", "subnets": []any{}, "routes": []any{}, "servers": []any{}, "labels": map[string]any{}, "created": "2026-10-23T10:00:00Z", "protection": map[string]any{"delete": false}}
	if _, _, err := c.EnsurePrivateNetwork(ctx, "mine", "fsn1"); err == nil || !strings.Contains(err.Error(), "did not create") {
		t.Errorf("foreign: %v", err)
	}
	if _, err := c.FindPrivateNetwork(ctx, "mine"); err == nil {
		t.Error("find must refuse a foreign network too")
	}
}

func TestEnsurePrivateNetworkInParallelHasOneCreator(t *testing.T) {
	a, c := newNetAPI(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	made := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, m, err := c.EnsurePrivateNetwork(context.Background(), "dev", "fsn1")
			if err != nil {
				t.Error(err)
			}
			if m {
				mu.Lock()
				made++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if made != 1 || a.creates != 1 {
		t.Errorf("created by %d callers, %d networks", made, a.creates)
	}
}

func TestAttachAndDeletePrivateNetwork(t *testing.T) {
	a, c := newNetAPI(t)
	ctx := context.Background()
	n, _, err := c.EnsurePrivateNetwork(ctx, "dev", "fsn1")
	if err != nil {
		t.Fatal(err)
	}
	ip, err := c.AttachPrivateNetwork(ctx, "5", n.ID)
	if err != nil || ip != "10.123.0.2" {
		t.Fatalf("attach: %q %v", ip, err)
	}
	if ip2, err := c.AttachPrivateNetwork(ctx, "5", n.ID); err != nil || ip2 != ip || len(a.servers[5]) != 1 {
		t.Errorf("attached already: %q %v (%d attachments)", ip2, err, len(a.servers[5]))
	}
	if _, err := c.AttachPrivateNetwork(ctx, "99", n.ID); err == nil {
		t.Error("unknown server")
	}
	// the network lists a server: deleting is refused
	id, _ := strconv.Atoi(n.ID)
	a.networks[id]["servers"] = []any{5}
	if err := c.DeletePrivateNetwork(ctx, n.ID); !errors.Is(err, cloud.ErrNetworkInUse) {
		t.Errorf("in use: %v", err)
	}
	a.networks[id]["servers"] = []any{}
	if err := c.DeletePrivateNetwork(ctx, n.ID); err != nil {
		t.Errorf("delete: %v", err)
	}
	if err := c.DeletePrivateNetwork(ctx, n.ID); err != nil {
		t.Errorf("gone already is no error: %v", err)
	}
}
