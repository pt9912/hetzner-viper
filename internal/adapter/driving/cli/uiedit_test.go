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
	ed := cli.UIEditor(&cli.Opts{Host: testHost(), Dir: dir}, env)
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
