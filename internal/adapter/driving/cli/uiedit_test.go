package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
	"github.com/pt9912/hetzner-viper/internal/adapter/driving/web"
)

func TestUIEditOptionsAndTheStart(t *testing.T) {
	if _, _, err := cli.Parse([]string{"ui", "--edit"}); err != nil {
		t.Error(err)
	}
	if _, _, err := cli.Parse([]string{"ui", "--edit=1"}); err == nil {
		t.Error("--edit takes no value")
	}
	// --readonly and --edit exclude each other: a start error, not a silent winner
	var out strings.Builder
	h := testHost()
	h.Stderr = &out
	if code := cli.Main("v9.9.9", []string{"ui", "--readonly", "--edit", "--no-open"}, h); code == 0 || !strings.Contains(out.String(), "exclude each other") {
		t.Errorf("code %d: %s", code, out.String())
	}
}

func TestTheEditorMakesAMachineAndLeavesATraceWithoutValues(t *testing.T) {
	env, home := nativeEnv(t)
	dir := t.TempDir()
	write(t, dir, ".env.local", "SERVER_PREFIX=ui\nDNS_PROVIDER=none\n")
	ed, _ := cli.UIEditor(&cli.Opts{Host: testHost(), Dir: dir}, env)
	tpl, err := ed.Templates(context.Background())
	if err != nil || !tpl.Base || len(tpl.Providers) != 6 {
		t.Fatalf("%+v %v", tpl, err)
	}
	req := web.EditRequest{Kind: "machine", Name: "box", Fields: map[string]string{"PROVIDER": "compose", "SERVER_LOCATION": "default", "SERVER_TYPE": "2x4", "COMPOSE_IMAGE": "x:1"}}
	pv, err := ed.Preview(context.Background(), req)
	if err != nil || len(pv.Problems) > 0 || len(pv.Files) != 1 {
		t.Fatalf("%+v %v", pv, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "machines", "box.env.local")); err == nil {
		t.Fatal("the preview wrote the file")
	}
	if _, err := ed.Create(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "machines", "box.env.local")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(home, ".local", "state", "viper", "events.log"))
	if !strings.Contains(string(b), "ui") || !strings.Contains(string(b), "made machines/box.env.local") || strings.Contains(string(b), "2x4") {
		t.Errorf("events.log: %q", b)
	}
	if _, err := ed.Create(context.Background(), req); err == nil {
		t.Error("a second create replaced the file")
	}
}

func TestTheEditorSetsACredentialAndNeverSaysItsValue(t *testing.T) {
	env, home := nativeEnv(t)
	dir := t.TempDir()
	write(t, dir, ".env.local", "SERVER_PREFIX=ui\nDNS_PROVIDER=none\nPROVIDER=hetzner\n")
	write(t, dir, "machines/web.env.local", "HCLOUD_TOKEN=old-token-0123456789\n")
	ed, redact := cli.UIEditor(&cli.Opts{Host: testHost(), Dir: dir}, env)
	const value = "brand-new-token-abcdef"
	tpl, err := ed.Templates(context.Background())
	if err != nil || len(tpl.Credentials) == 0 {
		t.Fatalf("%+v %v", tpl, err)
	}
	var set []string
	for _, f := range tpl.Files {
		if f.Target == "machine" && f.Name == "web" {
			set = f.Set
		}
		if f.Target == "base" && len(f.Set) != 0 {
			t.Errorf("nothing is set in the base file: %v", f.Set)
		}
	}
	if strings.Join(set, ",") != "HCLOUD_TOKEN" {
		t.Errorf("set: %v", set)
	}
	req := web.EditRequest{Kind: "credential", Target: "machine", Name: "web", Fields: map[string]string{"HCLOUD_TOKEN": value}}
	pv, err := ed.Preview(context.Background(), req)
	if err != nil || len(pv.Problems) > 0 || pv.Etag == "" {
		t.Fatalf("%+v %v", pv, err)
	}
	if redact("the token is "+value) != "the token is [hidden]" {
		t.Error("the redaction does not know the new value from the preview on")
	}
	req.Etag = pv.Etag
	res, err := ed.Create(context.Background(), req)
	if err != nil || res.Backup == "" {
		t.Fatalf("%+v %v", res, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "machines", "web.env.local"))
	if string(b) != "HCLOUD_TOKEN='"+value+"'\n" {
		t.Errorf("file %q", b)
	}
	log, _ := os.ReadFile(filepath.Join(home, ".local", "state", "viper", "events.log"))
	if !strings.Contains(string(log), "set HCLOUD_TOKEN in machines/web.env.local (backup "+res.Backup+")") || strings.Contains(string(log), value) {
		t.Errorf("events.log: %q", log)
	}
	// a repeat with the old state of the file is refused
	if _, err := ed.Create(context.Background(), req); err == nil {
		t.Error("a second apply of the same preview")
	}
}

func TestADuplicateTakesOverWhatTheFormKnowsAndNeverACredential(t *testing.T) {
	env, _ := nativeEnv(t)
	dir := t.TempDir()
	write(t, dir, ".env.local", "SERVER_PREFIX=ui\nDNS_PROVIDER=none\n")
	write(t, dir, "machines/web.env.local", "PROVIDER=compose\nSERVER_TYPE=2x4\nSERVER_LOCATION=default\nHCLOUD_TOKEN=tok-0123456789\nREPO_URL=https://github.com/x/y.git\nCOMPOSE_IMAGE=$IMG\nVIPER_POST_CLONE='a\nb'\n")
	write(t, dir, "machines/web-2.env.local", "")
	write(t, dir, "groups/dev.env", "GROUP_MACHINES=web\nNATS_ENABLED=true\nNATS_MCP=true\n")
	ed, _ := cli.UIEditor(&cli.Opts{Host: testHost(), Dir: dir}, env)
	c, err := ed.Duplicate(context.Background(), "machine", "web")
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "web-3" || c.Fields["PROVIDER"] != "compose" || c.Fields["SERVER_TYPE"] != "2x4" || len(c.Fields) != 3 {
		t.Errorf("%+v", c)
	}
	if strings.Join(c.Skipped, ",") != "HCLOUD_TOKEN,REPO_URL,COMPOSE_IMAGE,VIPER_POST_CLONE" {
		t.Errorf("skipped %v", c.Skipped)
	}
	g, err := ed.Duplicate(context.Background(), "group", "dev")
	if err != nil || g.Name != "dev-2" || g.Fields["NATS_ENABLED"] != "true" || strings.Join(g.Skipped, ",") != "GROUP_MACHINES,NATS_MCP" {
		t.Errorf("%+v %v", g, err)
	}
	for _, bad := range [][2]string{{"machine", "../x"}, {"machine", "nope"}, {"file", "web"}, {"machine", ""}, {"machine", "example"}} {
		if _, err := ed.Duplicate(context.Background(), bad[0], bad[1]); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}
