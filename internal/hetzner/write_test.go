package hetzner_test

import (
	"context"
	"encoding/json"
	"github.com/pt9912/hetzner-viper/internal/hetzner"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// writeAPI is a Hetzner project that records what is created.
type writeAPI struct {
	mu        sync.Mutex
	requests  []string
	bodies    map[string]map[string]any
	firewalls map[string][]map[string]any // name -> rules
	srv       *httptest.Server
}

func newWriteAPI(t *testing.T) *writeAPI {
	t.Helper()
	a := &writeAPI{bodies: map[string]map[string]any{}, firewalls: map[string][]map[string]any{"existing": {{"direction": "in", "protocol": "tcp", "port": "22", "source_ips": []string{"0.0.0.0/0"}, "destination_ips": []string{}}}}}
	a.srv = httptest.NewServer(http.HandlerFunc(a.handle))
	t.Cleanup(a.srv.Close)
	return a
}

func (a *writeAPI) client() *hetzner.Client { return hetzner.New("good", a.srv.URL) }

func (a *writeAPI) handle(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	key := r.Method + " " + r.URL.Path
	a.requests = append(a.requests, key)
	var body map[string]any
	if b, _ := io.ReadAll(r.Body); len(b) > 0 {
		_ = json.Unmarshal(b, &body)
		a.bodies[key] = body
	}
	reply := func(v map[string]any) { _ = json.NewEncoder(w).Encode(v) }
	action := map[string]any{"id": 7, "status": "success", "progress": 100, "command": "x", "started": "2026-01-01T00:00:00Z", "finished": "2026-01-01T00:00:01Z"}
	page := map[string]any{"meta": map[string]any{"pagination": map[string]any{"page": 1, "per_page": 50, "last_page": 1, "total_entries": 1}}}
	fwJSON := func(name string) map[string]any {
		rules := a.firewalls[name]
		if rules == nil {
			rules = []map[string]any{}
		}
		return map[string]any{"id": 11, "name": name, "rules": rules, "labels": map[string]any{}, "applied_to": []any{}, "created": "2026-01-01T00:00:00Z"}
	}
	switch {
	case key == "POST /ssh_keys":
		reply(map[string]any{"ssh_key": map[string]any{"id": 1, "name": body["name"], "fingerprint": "aa:bb", "public_key": body["public_key"], "labels": map[string]any{}, "created": "2026-01-01T00:00:00Z"}})
	case key == "GET /ssh_keys":
		if r.URL.Query().Get("name") == "claude-login" || r.URL.Query().Get("name") == "my laptop key" {
			page["ssh_keys"] = []any{map[string]any{"id": 5, "name": r.URL.Query().Get("name"), "fingerprint": "aa:bb", "public_key": "x", "labels": map[string]any{}, "created": "2026-01-01T00:00:00Z"}}
		} else {
			page["ssh_keys"] = []any{}
		}
		reply(page)
	case key == "GET /firewalls":
		name := r.URL.Query().Get("name")
		if _, ok := a.firewalls[name]; ok {
			page["firewalls"] = []any{fwJSON(name)}
		} else {
			page["firewalls"] = []any{}
		}
		reply(page)
	case key == "POST /firewalls":
		name := body["name"].(string)
		a.firewalls[name] = nil
		reply(map[string]any{"firewall": fwJSON(name), "actions": []any{action}})
	case key == "POST /firewalls/11/actions/set_rules":
		var rules []map[string]any
		for _, x := range body["rules"].([]any) {
			rules = append(rules, x.(map[string]any))
		}
		a.bodies["rules"] = map[string]any{"n": float64(len(rules))}
		reply(map[string]any{"actions": []any{action}})
	case key == "GET /server_types":
		if r.URL.Query().Get("name") == "cax11" {
			page["server_types"] = []any{map[string]any{"id": 105, "name": "cax11", "architecture": "arm", "cores": 2, "memory": 4, "disk": 40}}
		} else {
			page["server_types"] = []any{}
		}
		reply(page)
	case key == "GET /server_types/105":
		reply(map[string]any{"server_type": map[string]any{"id": 105, "name": "cax11", "architecture": "arm", "cores": 2, "memory": 4, "disk": 40}})
	case key == "GET /images/77":
		reply(map[string]any{"image": map[string]any{"id": 77, "name": "ubuntu-26.04", "architecture": "arm", "type": "system"}})
	case key == "GET /images":
		if r.URL.Query().Get("architecture") != "arm" || r.URL.Query().Get("name") != "ubuntu-26.04" {
			page["images"] = []any{}
		} else {
			page["images"] = []any{map[string]any{"id": 77, "name": "ubuntu-26.04", "architecture": "arm", "type": "system"}}
		}
		reply(page)
	case key == "POST /servers":
		running := map[string]any{"id": 7, "status": "running", "progress": 40, "command": "create_server", "started": "2026-01-01T00:00:00Z"}
		reply(map[string]any{
			"server": map[string]any{"id": 42, "name": body["name"], "created": "2026-10-23T10:00:00Z", "status": "initializing",
				"public_net": map[string]any{"ipv4": map[string]any{"ip": "203.0.113.7", "blocked": false, "dns_ptr": ""}, "ipv6": map[string]any{"ip": "2001:db8::/64"}, "floating_ips": []any{}}},
			"action": running, "next_actions": []any{}, "root_password": nil})
	case key == "GET /actions":
		page["actions"] = []any{action}
		reply(page)
	case strings.HasPrefix(key, "GET /actions/"):
		reply(map[string]any{"action": action})
	default:
		http.NotFound(w, r)
	}
}

func TestSSHKeyCreate(t *testing.T) {
	a := newWriteAPI(t)
	if err := a.client().SSHKeyCreate(context.Background(), "claude-login", "ssh-ed25519 AAAA me\n"); err != nil {
		t.Fatal(err)
	}
	b := a.bodies["POST /ssh_keys"]
	if b["name"] != "claude-login" || b["public_key"] != "ssh-ed25519 AAAA me" {
		t.Errorf("%v", b)
	}
}

func TestFirewallRulesAndAddRule(t *testing.T) {
	a := newWriteAPI(t)
	h := a.client()
	rules, exists, err := h.FirewallRules(context.Background(), "existing")
	if err != nil || !exists || len(rules) != 1 || rules[0].Direction != "in" || rules[0].Protocol != "tcp" || rules[0].Port != "22" || rules[0].SourceIPs[0] != "0.0.0.0/0" {
		t.Fatalf("%+v %v %v", rules, exists, err)
	}
	if _, exists, _ := h.FirewallRules(context.Background(), "missing"); exists {
		t.Error("missing firewall exists")
	}
	if err := h.FirewallCreate(context.Background(), "new"); err != nil {
		t.Fatal(err)
	}
	if rules, exists, _ := h.FirewallRules(context.Background(), "new"); !exists || len(rules) != 0 {
		t.Errorf("created firewall: %v %v", rules, exists)
	}
	// adding keeps the rule that is there
	if err := h.FirewallAddRule(context.Background(), "existing", hetzner.FirewallRule{Direction: "in", Protocol: "icmp", SourceIPs: []string{"0.0.0.0/0", "::/0"}}); err != nil {
		t.Fatal(err)
	}
	if a.bodies["rules"]["n"] != float64(2) {
		t.Errorf("rules sent: %v", a.bodies["rules"])
	}
	sent := a.bodies["POST /firewalls/11/actions/set_rules"]["rules"].([]any)
	if kept := sent[0].(map[string]any); kept["protocol"] != "tcp" || kept["port"] != "22" || kept["direction"] != "in" || kept["source_ips"].([]any)[0] != "0.0.0.0/0" {
		t.Errorf("the rule that was there is not kept as it was: %v", kept)
	}
	last := sent[1].(map[string]any)
	if last["protocol"] != "icmp" || last["direction"] != "in" || len(last["source_ips"].([]any)) != 2 {
		t.Errorf("new rule %v", last)
	}
	if err := h.FirewallAddRule(context.Background(), "existing", hetzner.FirewallRule{Direction: "in", Protocol: "tcp", Port: "22", SourceIPs: []string{"anywhere"}}); err == nil {
		t.Error("a bad network is refused before anything is sent")
	}
	if err := h.FirewallAddRule(context.Background(), "missing", hetzner.FirewallRule{Direction: "in", Protocol: "tcp"}); err == nil {
		t.Error("no firewall")
	}
}

func TestServerCreateUsesTheImageOfTheTypesArchitecture(t *testing.T) {
	a := newWriteAPI(t)
	a.firewalls["claude-ssh"] = nil
	s, err := a.client().ServerCreate(context.Background(), hetzner.ServerSpec{Name: "claude-web", Type: "cax11", Location: "fsn1", Image: "ubuntu-26.04", SSHKey: "claude-login", Firewall: "claude-ssh", UserData: "#cloud-config\nx: 1\n"})
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != 42 || s.IPv4 != "203.0.113.7" || s.Name != "claude-web" || !s.Created.Equal(time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("%+v", s)
	}
	b := a.bodies["POST /servers"]
	if b["name"] != "claude-web" || b["server_type"] != float64(105) || b["location"] != "fsn1" || b["user_data"] != "#cloud-config\nx: 1\n" {
		t.Errorf("body %v", b)
	}
	if b["image"] != float64(77) {
		t.Errorf("image %v", b["image"])
	}
	if keys := b["ssh_keys"].([]any); len(keys) != 1 || keys[0] != float64(5) {
		t.Errorf("ssh_keys %v", b["ssh_keys"])
	}
	if fws := b["firewalls"].([]any); len(fws) != 1 || fws[0].(map[string]any)["firewall"] != float64(11) {
		t.Errorf("firewalls %v", b["firewalls"])
	}
	// the actions of the create call are waited for
	waited := false
	for _, r := range a.requests {
		if strings.HasPrefix(r, "GET /actions") {
			waited = true
		}
	}
	if !waited {
		t.Errorf("no wait for the actions: %v", a.requests)
	}
	if _, err := a.client().ServerCreate(context.Background(), hetzner.ServerSpec{Name: "x", Type: "cax11", Location: "fsn1", Image: "nope", SSHKey: "claude-login", Firewall: "claude-ssh"}); err == nil || !strings.Contains(err.Error(), "image nope not found for arm") {
		t.Errorf("unknown image: %v", err)
	}
	if _, err := a.client().ServerCreate(context.Background(), hetzner.ServerSpec{Name: "x", Type: "cax11", Location: "fsn1", Image: "ubuntu-26.04", SSHKey: "no-key", Firewall: "claude-ssh"}); err == nil || !strings.Contains(err.Error(), "SSH key no-key not found") {
		t.Errorf("unknown key: %v", err)
	}
	if _, err := a.client().ServerCreate(context.Background(), hetzner.ServerSpec{Name: "x", Type: "cax11", Location: "fsn1", Image: "ubuntu-26.04", SSHKey: "claude-login", Firewall: "no-such"}); err == nil || !strings.Contains(err.Error(), "firewall no-such not found") {
		t.Errorf("unknown firewall: %v", err)
	}
}

func TestTypeLocationAndImageMayBeGivenByID(t *testing.T) {
	a := newWriteAPI(t)
	a.firewalls["claude-ssh"] = nil
	h := a.client()
	if st, err := h.ServerType(context.Background(), "105"); err != nil || st == nil || st.Cores != 2 {
		t.Errorf("type by ID: %+v %v", st, err)
	}
	if ok, err := h.ImageExists(context.Background(), "77", "arm"); !ok || err != nil {
		t.Errorf("image by ID: %v %v", ok, err)
	}
	if _, err := h.ServerCreate(context.Background(), hetzner.ServerSpec{Name: "x", Type: "105", Location: "fsn1", Image: "77", SSHKey: "claude-login", Firewall: "claude-ssh"}); err != nil {
		t.Errorf("create with IDs: %v", err)
	}
}
