package settingsfile_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/settingsfile"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

func writeIn(t *testing.T, dir, rel, text string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func groupEnvOf() settings.Env { return settings.Env{Lookup: envOf(nil), Home: "/h"} }

const groupBase = "DNS_DOMAIN=example.com\n"

func TestAMachineInAGroupFileGetsTheGroupAndItsSettings(t *testing.T) {
	dir := setupDir(t, groupBase+"SERVER_TYPE=cpx22\n", "")
	writeIn(t, dir, "groups/entwicklung.env", "GROUP_MACHINES=\"web, beta\"\nNATS_ENABLED=true\nSERVER_TYPE=cpx32\n")
	c, err := settingsfile.Load(dir, "web", groupEnvOf(), false)
	if err != nil {
		t.Fatal(err)
	}
	if c.Group != "entwicklung" || !c.NATS.Enabled || c.NATS.Group != "entwicklung" || c.ServerType != "cpx32" || !strings.HasSuffix(filepath.ToSlash(c.GroupFile), "groups/entwicklung.env") {
		t.Fatalf("%+v group=%q file=%q type=%s", c.NATS, c.Group, c.GroupFile, c.ServerType)
	}
	// the machine's own file wins over the group, and a machine without a file is fine
	writeIn(t, dir, "machines/web.env.local", "SERVER_TYPE=cax11\n")
	if c, err = settingsfile.Load(dir, "web", groupEnvOf(), false); err != nil || c.ServerType != "cax11" {
		t.Fatalf("%v %s", err, c.ServerType)
	}
	// a machine that no group names is as before
	if c, err = settingsfile.Load(dir, "other", groupEnvOf(), false); err != nil || c.Group != "" || c.NATS.Enabled || c.ServerType != "cpx22" {
		t.Fatalf("%v %+v", err, c)
	}
}

func TestWithoutAGroupsDirectoryNothingChanges(t *testing.T) {
	dir := setupDir(t, groupBase, "")
	c, err := settingsfile.Load(dir, "web", groupEnvOf(), false)
	if err != nil || c.Group != "" || len(c.Warnings) != 0 {
		t.Fatalf("%v %+v", err, c)
	}
}

func TestGroupFilesAreCheckedStrictly(t *testing.T) {
	for name, tc := range map[string]struct{ file, machine, want string }{
		"twice in the list": {"GROUP_MACHINES=web,web\n", "web", "web twice"},
		"bad name":          {"GROUP_MACHINES=web,Bad_Name\n", "web", "no machine name"},
		"other NATS_GROUP":  {"GROUP_MACHINES=web\nNATS_GROUP=anders\n", "web", "NATS_GROUP is anders"},
	} {
		dir := setupDir(t, groupBase, "")
		writeIn(t, dir, "groups/g.env", tc.file)
		_, err := settingsfile.Load(dir, tc.machine, groupEnvOf(), false)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
	}
}

func TestAMachineInTwoGroupsIsRefused(t *testing.T) {
	dir := setupDir(t, groupBase, "")
	writeIn(t, dir, "groups/a.env", "GROUP_MACHINES=web\n")
	writeIn(t, dir, "groups/b.env", "GROUP_MACHINES=beta,web\n")
	_, err := settingsfile.Load(dir, "web", groupEnvOf(), false)
	if err == nil || !strings.Contains(err.Error(), "a, b") {
		t.Fatalf("%v", err)
	}
	// the other machine of b is not affected
	if _, err := settingsfile.Load(dir, "beta", groupEnvOf(), false); err != nil {
		t.Errorf("beta is in one group only: %v", err)
	}
}

func TestAMachineFileMustNotNameAnotherNATSGroup(t *testing.T) {
	dir := setupDir(t, groupBase, "")
	writeIn(t, dir, "groups/g.env", "GROUP_MACHINES=web\nNATS_ENABLED=true\n")
	writeIn(t, dir, "machines/web.env.local", "NATS_GROUP=anders\n")
	if _, err := settingsfile.Load(dir, "web", groupEnvOf(), false); err == nil || !strings.Contains(err.Error(), "NATS_GROUP is anders") {
		t.Fatalf("%v", err)
	}
	writeIn(t, dir, "machines/web.env.local", "NATS_GROUP=g\n")
	if _, err := settingsfile.Load(dir, "web", groupEnvOf(), false); err != nil {
		t.Errorf("the same name is fine: %v", err)
	}
}

func TestABrokenFileOfAnotherGroupIsOnlyAWarningUnlessItNamesTheMachine(t *testing.T) {
	dir := setupDir(t, groupBase, "")
	writeIn(t, dir, "groups/fremd.env", "GROUP_MACHINES=Bad_Name,other\n")
	c, err := settingsfile.Load(dir, "web", groupEnvOf(), false)
	if err != nil || len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], "groups/fremd.env is ignored") {
		t.Fatalf("%v %v", err, c)
	}
	// the same file, but it names this machine: the machine would lose its group silently
	writeIn(t, dir, "groups/fremd.env", "GROUP_MACHINES=Bad_Name,web\n")
	if _, err := settingsfile.Load(dir, "web", groupEnvOf(), false); err == nil {
		t.Error("a broken file that names the machine must stop it")
	}
	// a file name that is no group name is a warning too
	dir2 := setupDir(t, groupBase, "")
	writeIn(t, dir2, "groups/Bad.Name.env", "GROUP_MACHINES=x\n")
	if c, err := settingsfile.Load(dir2, "web", groupEnvOf(), false); err != nil || len(c.Warnings) != 1 {
		t.Errorf("%v %v", err, c)
	}
}

func TestPrefixKnowsTheGroupFile(t *testing.T) {
	dir := setupDir(t, groupBase+"SERVER_PREFIX=a\n", "")
	writeIn(t, dir, "groups/g.env", "GROUP_MACHINES=web\nSERVER_PREFIX=lab\n")
	if got, _ := settingsfile.Prefix(dir, "web", groupEnvOf()); got != "lab" {
		t.Errorf("group: %s", got)
	}
	if got, _ := settingsfile.Prefix(dir, "x", groupEnvOf()); got != "a" {
		t.Errorf("no group: %s", got)
	}
	writeIn(t, dir, "machines/web.env.local", "SERVER_PREFIX=own\n")
	if got, _ := settingsfile.Prefix(dir, "web", groupEnvOf()); got != "own" {
		t.Errorf("machine: %s", got)
	}
}

func TestLoadGroupReadsTheMachinesInOrder(t *testing.T) {
	dir := setupDir(t, groupBase, "")
	writeIn(t, dir, "groups/g.env", "GROUP_MACHINES=\" gamma , alpha,beta \"\nNATS_ENABLED=true\n")
	g, err := settingsfile.LoadGroup(dir, "g", groupEnvOf())
	if err != nil || !reflect.DeepEqual(g.Machines, []string{"gamma", "alpha", "beta"}) || g.Name != "g" {
		t.Fatalf("%v %+v", err, g)
	}
	if _, err := settingsfile.LoadGroup(dir, "nope", groupEnvOf()); err == nil || !strings.Contains(err.Error(), "No group nope") {
		t.Errorf("%v", err)
	}
	if _, err := settingsfile.LoadGroup(dir, "Bad.Name", groupEnvOf()); err == nil {
		t.Error("bad group name")
	}
}
