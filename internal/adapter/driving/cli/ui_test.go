package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
	"github.com/pt9912/hetzner-viper/internal/adapter/driving/web"
)

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func uiDir(t *testing.T) string {
	dir := t.TempDir()
	write(t, dir, ".env.local", "DNS_DOMAIN=example.com\nSERVER_PREFIX=ui\nHCLOUD_TOKEN=hc-token-0123456789\n")
	write(t, dir, "machines/web.env.local", "SERVER_TYPE=cpx22\n")
	write(t, dir, "machines/lone.env.local", "SERVER_TYPE=cpx32\n")
	write(t, dir, "machines/example.env.local.example", "")
	write(t, dir, "groups/dev.env", "GROUP_MACHINES=web\n")
	write(t, dir, "groups/broken.env", "GROUP_MACHINES=Not A Name\n")
	return dir
}

func TestTheOverviewListsMachinesAndGroupsFromTheFiles(t *testing.T) {
	env, _ := nativeEnv(t)
	dir := uiDir(t)
	b, _ := cli.UIBackend(&cli.Opts{Host: testHost(), Dir: dir}, env)
	ov, err := b.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range ov.Machines {
		got = append(got, m.Name+"/"+m.Group)
		if m.Error != "" {
			t.Errorf("%s: %s", m.Name, m.Error)
		}
	}
	if strings.Join(got, " ") != "lone/ web/dev" {
		t.Errorf("machines %v", got)
	}
	var web0 web.Machine
	for _, m := range ov.Machines {
		if m.Name == "web" {
			web0 = m
		}
	}
	if web0.Provider != "hetzner" || web0.Type != "cpx22" || web0.State != "none" {
		t.Errorf("web: %+v", web0)
	}
	if len(ov.Groups) != 2 || ov.Groups[0].Name != "broken" || ov.Groups[0].Error == "" || ov.Groups[1].Name != "dev" || strings.Join(ov.Groups[1].Machines, ",") != "web" {
		t.Errorf("groups: %+v", ov.Groups)
	}
}

func TestTheStateIsWhatTheMarksSay(t *testing.T) {
	env, _ := nativeEnv(t)
	dir := uiDir(t)
	b, _ := cli.UIBackend(&cli.Opts{Host: testHost(), Dir: dir}, env)
	state := func() string {
		ov, _ := b.Overview(context.Background())
		for _, m := range ov.Machines {
			if m.Name == "lone" {
				return m.State
			}
		}
		return "?"
	}
	if s := state(); s != "none" {
		t.Fatal(s)
	}
	marks := filepath.Join(env.Home, ".local", "state", "viper", "servers", "ui")
	write(t, marks, "id-ui-lone", "123\n")
	if s := state(); s != "started" {
		t.Errorf("with an id: %s", s)
	}
	write(t, marks, "ready-ui-lone", "123\n")
	if s := state(); s != "ready" {
		t.Errorf("with both: %s", s)
	}
}

func TestTheRedactionTakesTheValuesOfTheSecretsOut(t *testing.T) {
	env, _ := nativeEnv(t)
	dir := uiDir(t)
	write(t, dir, "machines/web.env.local", "SERVER_TYPE=cpx22\nVIPER_GH_TOKEN=ghp_machinesecret99\n")
	b, redact := cli.UIBackend(&cli.Opts{Host: testHost(), Dir: dir}, env)
	if _, err := b.Overview(context.Background()); err != nil { // learns the secrets of the machines
		t.Fatal(err)
	}
	got := redact("a hc-token-0123456789 b ghp_machinesecret99 c short")
	if strings.Contains(got, "hc-token") || strings.Contains(got, "ghp_") || !strings.Contains(got, "[hidden]") || !strings.Contains(got, "c short") {
		t.Errorf("%q", got)
	}
}

func TestTheLogPageShowsWhatViperLogShows(t *testing.T) {
	env, _ := nativeEnv(t)
	dir := uiDir(t)
	events := filepath.Join(env.Home, ".local", "state", "viper")
	write(t, events, "events.log", "2026-11-14T10:00:00+00:00 ui-web   up   start\n")
	b, _ := cli.UIBackend(&cli.Opts{Host: testHost(), Dir: dir}, env)
	var sb strings.Builder
	if err := b.Log(context.Background(), "", false, &sb); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), "ui-web") {
		t.Errorf("%q", sb.String())
	}
}

func TestTheBrowserIsOpenedWithoutAShell(t *testing.T) {
	for goos, want := range map[string]string{"linux": "xdg-open", "darwin": "open", "windows": "rundll32", "freebsd": "xdg-open"} {
		name, args := cli.BrowserCommand(goos, "/tmp/x/start.html")
		if name != want || args[len(args)-1] != "/tmp/x/start.html" {
			t.Errorf("%s: %s %v", goos, name, args)
		}
		if name == "cmd" {
			t.Errorf("%s: cmd reads the & of an address", goos)
		}
	}
	if runtime.GOOS == "" {
		t.Skip()
	}
}

func TestUIOptions(t *testing.T) {
	for _, bad := range [][]string{{"ui", "--port", "0"}, {"ui", "--port", "99999"}, {"ui", "--port", "x"}, {"ui", "--port"}, {"ui", "web"}, {"ui", "--no-open=1"}} {
		if _, _, err := cli.Parse(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if _, _, err := cli.Parse([]string{"ui", "--port", "8787", "--no-open", "--readonly"}); err != nil {
		t.Error(err)
	}
}

func TestTheOverviewNamesTheNATSGroupsAndWhetherThereIsAnOperatorKey(t *testing.T) {
	env, home := nativeEnv(t)
	dir := t.TempDir()
	write(t, dir, ".env.local", "PROVIDER=compose\nSERVER_PREFIX=ui\nSERVER_LOCATION=default\nCOMPOSE_IMAGE=x:1\nDNS_PROVIDER=none\n")
	for _, m := range []string{"a", "b"} {
		write(t, dir, "machines/"+m+".env.local", "NATS_ENABLED=true\nNATS_GROUP=dev\nNATS_MCP=true\n")
	}
	write(t, dir, "machines/plain.env.local", "")
	write(t, dir, "groups/dev.env", "GROUP_MACHINES=a,b\n")
	write(t, dir, "groups/other.env", "GROUP_MACHINES=plain\n")
	b, _ := cli.UIBackend(&cli.Opts{Host: testHost(), Dir: dir}, env)
	ov, err := b.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ov.NATS) != 1 || ov.NATS[0].Name != "dev" || strings.Join(ov.NATS[0].Machines, ",") != "a,b" || ov.NATS[0].Operator {
		t.Fatalf("nats groups: %+v", ov.NATS)
	}
	write(t, filepath.Join(home, ".local", "state", "viper"), "groups/dev/operator.nk", "SUAXXXX")
	ov, _ = b.Overview(context.Background())
	if !ov.NATS[0].Operator {
		t.Errorf("the key is there: %+v", ov.NATS)
	}
}

func TestTheSettingsShowNamesAndOnlyTheValuesOfThePositiveList(t *testing.T) {
	env, _ := nativeEnv(t)
	dir := t.TempDir()
	write(t, dir, ".env.local", "# a comment SERVER_TYPE=hidden\nexport PROVIDER=hetzner\nHCLOUD_TOKEN=tok-0123456789\nSERVER_TYPE=cpx22   # note\nNATS_MCP_TOPICS=\"build news\"\nDNS_DOMAIN=$DOMAIN\nK3D_CLUSTER_NAME='kc'\nAWS_ROOT_GB=40\nVIPER_GH_TOKEN=\nREPO_URL=https://user:pw@example.org/r.git\n")
	write(t, dir, "groups/dev.env", "GROUP_MACHINES=a,b\n")
	write(t, dir, "machines/a.env.local", "SERVER_LOCATION=nbg1\n")
	write(t, dir, "machines/example.env.local.example", "SERVER_TYPE=x\n")
	env.Lookup = func(k string) (string, bool) {
		v, ok := map[string]string{"VIPER_CLAUDE_TOKEN": "claude-0123456789", "SERVER_IMAGE": "ubuntu"}[k]
		return v, ok
	}
	files, err := cli.UISettings(&cli.Opts{Host: testHost(), Dir: dir}, env)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	got := map[string]string{}
	shown := map[string]bool{}
	for _, f := range files {
		names = append(names, f.Name)
		for _, s := range f.Settings {
			got[f.Name+" "+s.Key] = s.Value
			shown[f.Name+" "+s.Key] = s.Shown
		}
	}
	if strings.Join(names, ",") != ".env.local,groups/dev.env,machines/a.env.local,environment" {
		t.Errorf("files %v", names)
	}
	for k, want := range map[string]string{".env.local PROVIDER": "hetzner", ".env.local SERVER_TYPE": "cpx22", ".env.local NATS_MCP_TOPICS": "build news", ".env.local K3D_CLUSTER_NAME": "kc", ".env.local AWS_ROOT_GB": "40", "groups/dev.env GROUP_MACHINES": "a,b", "machines/a.env.local SERVER_LOCATION": "nbg1"} {
		if got[k] != want || !shown[k] {
			t.Errorf("%s = %q (shown %v), want %q", k, got[k], shown[k], want)
		}
	}
	for _, k := range []string{".env.local HCLOUD_TOKEN", ".env.local DNS_DOMAIN", ".env.local VIPER_GH_TOKEN", ".env.local REPO_URL"} {
		if _, ok := got[k]; !ok || got[k] != "" || shown[k] {
			t.Errorf("%s must be a name only: %q %v", k, got[k], shown[k])
		}
	}
	if _, ok := got[".env.local # a"]; ok {
		t.Error("a comment is no setting")
	}
	var envNames []string
	for _, f := range files {
		if f.Name == "environment" {
			for _, s := range f.Settings {
				envNames = append(envNames, s.Key)
				if s.Value != "" {
					t.Errorf("a value from the environment is shown: %v", s)
				}
			}
		}
	}
	if strings.Join(envNames, ",") != "SERVER_IMAGE,VIPER_CLAUDE_TOKEN" {
		t.Errorf("environment %v", envNames)
	}
}
