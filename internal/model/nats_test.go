package model_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/model"
)

func TestCheckNATSName(t *testing.T) {
	long := "a234567890123456789012345678901" // 31
	for name, ok := range map[string]bool{
		"entwicklung": true, "a": true, "a-b": true, "x1": true, long: true, long + "b": false,
		"": false, "-a": false, "a-": false, "A": false, "a.b": false, "a*": false, "a>": false,
		"a b": false, "a_b": false, "ä": false,
	} {
		if err := model.CheckNATSName("NATS_GROUP", name); (err == nil) != ok {
			t.Errorf("%q: ok=%v, err=%v", name, ok, err)
		}
	}
}

func TestParseNATSBool(t *testing.T) {
	for in, want := range map[string]bool{"true": true, "TRUE": true, "1": true, " true ": true, "": false, "0": false, "false": false} {
		if got, err := model.ParseNATSBool("NATS_ENABLED", in); err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	for _, in := range []string{"yes", "on", "2"} {
		if _, err := model.ParseNATSBool("NATS_ENABLED", in); err == nil {
			t.Errorf("%q must be refused", in)
		}
	}
}

func TestParseNATSPort(t *testing.T) {
	if p, err := model.ParseNATSPort(""); err != nil || p != model.DefaultNATSPort {
		t.Errorf("default: %d %v", p, err)
	}
	if p, err := model.ParseNATSPort("4333"); err != nil || p != 4333 {
		t.Errorf("4333: %d %v", p, err)
	}
	for _, in := range []string{"22", "70000", "x", "-1", "1023"} {
		if _, err := model.ParseNATSPort(in); err == nil {
			t.Errorf("%q must be refused", in)
		}
	}
}

func TestNATSLabelsRoundTrip(t *testing.T) {
	in := model.NATSMetadata{Group: "dev", Role: model.NATSServer, State: model.NATSReady}
	out, ok, err := model.NATSMetadataFromLabels(model.NATSLabels(in))
	if err != nil || !ok || out != in {
		t.Fatalf("%+v %v %v", out, ok, err)
	}
	if _, ok, err := model.NATSMetadataFromLabels(map[string]string{"team": "x"}); ok || err != nil {
		t.Errorf("a server without NATS labels is no member and no error: %v %v", ok, err)
	}
}

func TestNATSMetadataFromLabelsRefusesNonsense(t *testing.T) {
	good := model.NATSLabels(model.NATSMetadata{Group: "dev", Role: model.NATSClient, State: model.NATSFailed})
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range good {
			m[a] = b
		}
		if v == "" {
			delete(m, k)
		} else {
			m[k] = v
		}
		return m
	}
	for name, l := range map[string]map[string]string{
		"role":     with(model.LabelNATSRole, "boss"),
		"state":    with(model.LabelNATSState, "done"),
		"group":    with(model.LabelNATSGroup, "A.b"),
		"no role":  with(model.LabelNATSRole, ""),
		"no state": with(model.LabelNATSState, ""),
		"no group": with(model.LabelNATSGroup, ""),
	} {
		if _, ok, err := model.NATSMetadataFromLabels(l); ok || err == nil {
			t.Errorf("%s: ok=%v err=%v", name, ok, err)
		}
	}
}

func TestMCPAllowedToolsFollowTheMode(t *testing.T) {
	if got := model.MCPAllowedTools(model.AutoAllowOff); len(got) != 0 {
		t.Errorf("off: %v", got)
	}
	if got := strings.Join(model.MCPAllowedTools(model.AutoAllowRead), ","); got != "peers,inbox,topics" {
		t.Errorf("read: %s", got)
	}
	if got := model.MCPAllowedTools(model.AutoAllowAll); len(got) != 10 || len(got) != len(model.MCPTools()) {
		t.Errorf("all: %v", got)
	}
	if got := model.MCPAllowedTools("anything"); got != nil {
		t.Errorf("an unknown mode allows nothing: %v", got)
	}
}

// A key that an older version put in nats/<group>/ is found in groups/<group>/.
func TestOperatorKeyFileMovesAnOldKey(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "nats", "dev", "operator.nk")
	if err := os.MkdirAll(filepath.Dir(old), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("seed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := model.OperatorKeyFile(dir, "dev")
	if want := filepath.Join(dir, "groups", "dev", "operator.nk"); path != want {
		t.Fatalf("%s, want %s", path, want)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "seed\n" {
		t.Errorf("the key was not moved: %q %v", b, err)
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("the old key is still there")
	}
}

// The state directory of versions up to v0.21.1 is moved to ~/.local/state/viper as a whole.
func TestStateDirMovesTheOldDirectory(t *testing.T) {
	home := t.TempDir()
	old := filepath.Join(home, ".local", "state", "hetzner-claude")
	if err := os.MkdirAll(filepath.Join(old, "servers", "claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "events.log"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := model.StateDir(home)
	if want := filepath.Join(home, ".local", "state", "viper"); dir != want {
		t.Fatalf("%s, want %s", dir, want)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "events.log")); err != nil || string(b) != "x\n" {
		t.Errorf("not moved: %q %v", b, err)
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("the old directory is still there")
	}
	// a second call and a new directory next to an old one: nothing is merged or lost
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if model.StateDir(home) != dir {
		t.Error("changed")
	}
	if _, err := os.Stat(old); err != nil {
		t.Error("an old directory next to a new one is left alone")
	}
}
