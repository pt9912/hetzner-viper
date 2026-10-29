package cli_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

type fakeFind struct {
	need  *model.Need
	shown int
}

func (f fakeFind) Find(_ context.Context, n model.Need, out, _ io.Writer) int {
	*f.need = n
	if f.shown > 0 {
		_, _ = io.WriteString(out, "a table\n")
	}
	return f.shown
}

func TestFindReadsTheNeedFromTheOptions(t *testing.T) {
	name, o, err := cli.Parse([]string{"find", "--cpu", "4", "--ram=16", "--disk", "80", "--arch", "arm", "--location", "fsn1", "--vat", "7.5"})
	if err != nil || name != "find" || o.CPU != 4 || o.RAM != 16 || o.Disk != 80 || o.Arch != "arm" || o.Location != "fsn1" || !o.HasVAT || o.VAT != 7.5 {
		t.Fatalf("%s %+v %v", name, o, err)
	}
	for _, args := range [][]string{{"find", "--cpu", "0"}, {"find", "--cpu", "x"}, {"find", "--cpu", "2.5"}, {"find", "--ram", "-1"}, {"find", "--vat", "-1"}, {"find", "--vat", "101"}, {"find", "--vat", "NaN"}, {"find", "--ram", "NaN"}, {"find", "--ram", "Inf"}, {"find", "web"}} {
		if _, _, err := cli.Parse(args); err == nil {
			t.Errorf("%v: want an error", args)
		}
	}
}

func TestFindAsksForAtLeastOneNeed(t *testing.T) {
	env, _ := nativeEnv(t)
	err := cli.RunFind(context.Background(), &cli.Opts{Host: testHost(), Dir: t.TempDir()}, env, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "--cpu") {
		t.Errorf("%v", err)
	}
	err = cli.RunFind(context.Background(), &cli.Opts{Host: testHost(), Dir: t.TempDir(), CPU: 1, Arch: "mips"}, env, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "x86 or arm") {
		t.Errorf("%v", err)
	}
}

func TestFindPassesTheNeedAndTheCredentials(t *testing.T) {
	env, _ := nativeEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("HCLOUD_TOKEN=tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var got model.Need
	var token string
	h := testHost()
	h.Services.Find = func(get func(string) string, _ io.Writer) driving.Find {
		token = get("HCLOUD_TOKEN")
		return fakeFind{need: &got, shown: 1}
	}
	var out bytes.Buffer
	err := cli.RunFind(context.Background(), &cli.Opts{Host: h, Dir: dir, CPU: 4, RAM: 16, Disk: 80}, env, &out, io.Discard)
	if err != nil || token != "tok" || got != (model.Need{VCPU: 4, RAMGB: 16, DiskGB: 80}) || out.String() != "a table\n" {
		t.Errorf("%v %q %+v %q", err, token, got, out.String())
	}
}

func TestFindFailsIfNothingIsShown(t *testing.T) {
	env, _ := nativeEnv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h := testHost()
	h.Services.Find = func(func(string) string, io.Writer) driving.Find { return fakeFind{need: new(model.Need)} }
	err := cli.RunFind(context.Background(), &cli.Opts{Host: h, Dir: dir, CPU: 4}, env, io.Discard, io.Discard)
	if cli.ExitCode(err) != 1 {
		t.Errorf("%v", err)
	}
}
