package hetzner_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/hetzner"
	"github.com/pt9912/hetzner-viper/internal/model"
)

// natsAPI is a Hetzner project with servers that carry labels.
type natsAPI struct {
	mu       sync.Mutex
	servers  map[int]map[string]any // id -> server JSON
	selector string                 // label_selector of the last list
	updated  map[string]any         // body of the last PUT
}

func newNATSAPI(t *testing.T, servers ...map[string]any) (*natsAPI, *hetzner.Client) {
	t.Helper()
	a := &natsAPI{servers: map[int]map[string]any{}}
	for _, s := range servers {
		a.servers[int(s["id"].(int))] = s
	}
	srv := httptest.NewServer(http.HandlerFunc(a.handle))
	t.Cleanup(srv.Close)
	return a, hetzner.New("good", srv.URL)
}

func server(id int, name string, labels map[string]string, privateIP string) map[string]any {
	s := map[string]any{"id": id, "name": name, "created": "2026-10-23T10:00:00Z", "status": "running", "labels": labels,
		"public_net":  map[string]any{"ipv4": map[string]any{"ip": "203.0.113.7", "blocked": false, "dns_ptr": ""}, "ipv6": map[string]any{"ip": "2001:db8::/64"}, "floating_ips": []any{}},
		"private_net": []any{}}
	if privateIP != "" {
		s["private_net"] = []any{map[string]any{"network": 1, "ip": privateIP, "alias_ips": []any{}, "mac_address": "86:00:00:00:00:01"}}
	}
	return s
}

func (a *natsAPI) handle(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == "GET" && r.URL.Path == "/servers":
		a.selector = r.URL.Query().Get("label_selector")
		var list []any
		for _, s := range a.servers { // the fake only knows "key=value" pairs joined by commas
			if matches(s["labels"].(map[string]string), a.selector) {
				list = append(list, s)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"servers": list, "meta": map[string]any{"pagination": map[string]any{"page": 1, "per_page": 50, "last_page": 1, "total_entries": len(list)}}})
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/servers/"):
		for id, s := range a.servers {
			if r.URL.Path == "/servers/"+itoa(id) {
				_ = json.NewEncoder(w).Encode(map[string]any{"server": s})
				return
			}
		}
		w.WriteHeader(404)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "not_found", "message": "server not found"}})
	case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/servers/"):
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &a.updated)
		for id, s := range a.servers {
			if r.URL.Path == "/servers/"+itoa(id) {
				labels := map[string]string{}
				for k, v := range a.updated["labels"].(map[string]any) {
					labels[k] = v.(string)
				}
				s["labels"] = labels
				_ = json.NewEncoder(w).Encode(map[string]any{"server": s})
				return
			}
		}
		http.NotFound(w, r)
	default:
		http.NotFound(w, r)
	}
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

func matches(labels map[string]string, selector string) bool {
	for _, kv := range strings.Split(selector, ",") {
		k, v, _ := strings.Cut(kv, "=")
		if labels[k] != v {
			return false
		}
	}
	return true
}

func lbl(group, role, state string) map[string]string {
	return map[string]string{model.LabelManaged: "true", model.LabelNATSGroup: group, model.LabelNATSRole: role, model.LabelNATSState: state}
}

func TestListGroupMachinesAsksForTheGroupAndReadsRoleAndState(t *testing.T) {
	a, c := newNATSAPI(t,
		server(1, "claude-alpha", lbl("dev", "server", "ready"), "10.0.0.2"),
		server(2, "claude-beta", lbl("dev", "client", "provisioning"), ""),
		server(3, "claude-other", lbl("prod", "server", "ready"), "10.1.0.2"),
		server(4, "claude-plain", map[string]string{}, ""),
	)
	got, err := c.ListGroupMachines(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	if a.selector != "viper-managed=true,viper-nats-group=dev" {
		t.Errorf("selector %q", a.selector)
	}
	if len(got) != 2 {
		t.Fatalf("got %d members: %+v", len(got), got)
	}
	by := map[string]model.GroupMember{}
	for _, m := range got {
		by[m.Server.Name] = m
	}
	al := by["claude-alpha"]
	if al.NATS.Role != model.NATSServer || al.NATS.State != model.NATSReady || al.Server.PrivateIP != "10.0.0.2" || al.Server.ID != "1" {
		t.Errorf("alpha: %+v", al)
	}
	if be := by["claude-beta"]; be.NATS.Role != model.NATSClient || be.NATS.State != model.NATSProvisioning || be.Server.PrivateIP != "" {
		t.Errorf("beta: %+v", be)
	}
}

func TestListGroupMachinesRefusesNonsenseLabelsAndBadGroups(t *testing.T) {
	_, c := newNATSAPI(t, server(1, "claude-x", lbl("dev", "boss", "ready"), ""))
	if _, err := c.ListGroupMachines(context.Background(), "dev"); err == nil || !strings.Contains(err.Error(), "claude-x") || !strings.Contains(err.Error(), "server or client") {
		t.Errorf("unknown role: %v", err)
	}
	half := lbl("dev", "server", "ready")
	delete(half, model.LabelNATSState)
	_, c = newNATSAPI(t, server(1, "claude-y", half, ""))
	if _, err := c.ListGroupMachines(context.Background(), "dev"); err == nil || !strings.Contains(err.Error(), "all be there") {
		t.Errorf("half-labelled: %v", err)
	}
	if _, err := c.ListGroupMachines(context.Background(), "a,b=c"); err == nil {
		t.Error("a group name that could change the selector must be refused")
	}
}

func TestUpdateNATSMetadataKeepsTheOtherLabels(t *testing.T) {
	a, c := newNATSAPI(t, server(1, "claude-alpha", map[string]string{"team": "x", model.LabelNATSState: "old"}, ""))
	meta := model.NATSMetadata{Group: "dev", Role: model.NATSServer, State: model.NATSReady}
	if err := c.UpdateNATSMetadata(context.Background(), "1", meta); err != nil {
		t.Fatal(err)
	}
	got := a.updated["labels"].(map[string]any)
	want := map[string]any{"team": "x", model.LabelManaged: "true", model.LabelNATSGroup: "dev", model.LabelNATSRole: "server", model.LabelNATSState: "ready"}
	if len(got) != len(want) {
		t.Fatalf("labels %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("label %s = %v, want %v", k, got[k], v)
		}
	}
	if err := c.UpdateNATSMetadata(context.Background(), "99", meta); err == nil {
		t.Error("an unknown server must be an error")
	}
	if err := c.UpdateNATSMetadata(context.Background(), "x", meta); err == nil {
		t.Error("not a server ID")
	}
}

func TestServerCreateSetsTheLabels(t *testing.T) {
	a := newWriteAPI(t)
	a.firewalls["claude-ssh"] = nil
	labels := model.NATSLabels(model.NATSMetadata{Group: "dev", Role: model.NATSClient, State: model.NATSProvisioning})
	if _, err := a.client().CreateServer(context.Background(), model.ServerSpec{Name: "claude-web", Size: "cax11", Region: "fsn1", Image: "ubuntu-26.04", SSHKey: "claude-login", Firewall: "claude-ssh", Labels: labels}); err != nil {
		t.Fatal(err)
	}
	got, _ := a.bodies["POST /servers"]["labels"].(map[string]any)
	if got[model.LabelNATSGroup] != "dev" || got[model.LabelManaged] != "true" || len(got) != 4 {
		t.Errorf("labels %v", got)
	}
	// without labels the request is as it was before
	if _, err := a.client().CreateServer(context.Background(), model.ServerSpec{Name: "claude-web", Size: "cax11", Region: "fsn1", Image: "ubuntu-26.04", SSHKey: "claude-login", Firewall: "claude-ssh"}); err != nil {
		t.Fatal(err)
	}
	if _, has := a.bodies["POST /servers"]["labels"]; has {
		t.Errorf("no labels expected: %v", a.bodies["POST /servers"]["labels"])
	}
}
