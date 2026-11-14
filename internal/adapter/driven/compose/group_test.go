package compose_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/compose"
	"github.com/pt9912/hetzner-viper/internal/model"
)

func groupSpec(name, group string) model.ServerSpec {
	s := spec(name)
	s.Group = group
	return s
}

func fileOf(t *testing.T, dir, key string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "compose", key, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// The machines of a group share one project and one file; every machine has keys and volumes of its own in it.
func TestTheMachinesOfAGroupShareOneProjectAndOneFile(t *testing.T) {
	ctx := context.Background()
	h := newHost()
	c, dir := newClient(t, h)
	for _, n := range []string{"claude-w1", "claude-w2", "claude-w3"} {
		if _, err := c.CreateServer(ctx, groupSpec(n, "dev")); err != nil {
			t.Fatal(err)
		}
	}
	text := fileOf(t, dir, "g_dev")
	for _, want := range []string{"claude-w1:", "claude-w2-docker:", "claude-w3-home:", "./secret/claude-w2/host_key", "service:claude-w1"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q is not in the file:\n%s", want, text)
		}
	}
	if strings.Contains(text, "name: viper") || strings.Contains(text, "\n    machine:\n") {
		t.Errorf("the file of a group has the name of no project and no service 'machine':\n%s", text)
	}
	for _, n := range []string{"claude-w1", "claude-w2", "claude-w3"} {
		if exists(filepath.Join(dir, "compose", n)) {
			t.Errorf("a directory of its own for %s", n)
		}
		if !exists(filepath.Join(dir, "compose", "g_dev", "secret", n, "authorized_keys")) {
			t.Errorf("no keys of %s", n)
		}
		if h.containers[n].labels["viper-project"] != "g_dev" {
			t.Errorf("%s: %v", n, h.containers[n].labels)
		}
	}
	for _, p := range h.projects {
		if p != "viper_g_dev" {
			t.Errorf("project %q", p)
		}
	}
}

// Deleting one machine of a group leaves the others and their keys alone; the last one takes the directory with it.
func TestDeletingAMachineOfAGroupLeavesTheOthers(t *testing.T) {
	ctx := context.Background()
	h := newHost()
	c, dir := newClient(t, h)
	var ids []string
	for _, n := range []string{"claude-w1", "claude-w2"} {
		s, err := c.CreateServer(ctx, groupSpec(n, "dev"))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, s.ID)
	}
	if err := c.DeleteServer(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if h.containers["claude-w1"] != nil || h.containers["claude-w1-docker"] != nil {
		t.Error("the deleted machine is still there")
	}
	if h.containers["claude-w2"] == nil || h.containers["claude-w2-docker"] == nil {
		t.Error("the other machine was removed")
	}
	if _, ok := h.volumes["claude-w1-home"]; ok {
		t.Error("the volume of the deleted machine stays")
	}
	if _, ok := h.volumes["claude-w2-home"]; !ok {
		t.Error("the volume of the other machine was removed")
	}
	for v := range h.volumes {
		if v == "anon-claude-w1-docker" {
			t.Error("the anonymous volume of the daemon stays")
		}
	}
	text := fileOf(t, dir, "g_dev")
	if strings.Contains(text, "claude-w1") || !strings.Contains(text, "claude-w2") {
		t.Errorf("the file after deleting w1:\n%s", text)
	}
	if exists(filepath.Join(dir, "compose", "g_dev", "secret", "claude-w1")) || !exists(filepath.Join(dir, "compose", "g_dev", "secret", "claude-w2", "host_key")) {
		t.Error("the keys of the deleted machine stay, or those of the other are gone")
	}
	if err := c.DeleteServer(ctx, ids[1]); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dir, "compose", "g_dev")) {
		t.Error("the directory of the group stays after the last machine")
	}
	if len(h.containers) != 0 || len(h.volumes) != 0 {
		t.Errorf("%v %v", h.containers, h.volumes)
	}
}

// A machine that is in no group has its own project, next to a group's.
func TestAMachineOfItsOwnLivesNextToAGroup(t *testing.T) {
	ctx := context.Background()
	h := newHost()
	c, dir := newClient(t, h)
	own, err := c.CreateServer(ctx, spec("claude-solo"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateServer(ctx, groupSpec("claude-w1", "dev")); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(dir, "compose", "claude-solo", "compose.yaml")) || h.containers["claude-solo"].labels["viper-project"] != "claude-solo" {
		t.Error("the machine of its own has its own project")
	}
	if err := c.DeleteServer(ctx, own.ID); err != nil {
		t.Fatal(err)
	}
	if h.containers["claude-w1"] == nil || !exists(filepath.Join(dir, "compose", "g_dev", "compose.yaml")) {
		t.Error("the group was touched")
	}
}

// Starting a stopped machine of a group starts its services only.
func TestAStoppedMachineOfAGroupIsStartedAlone(t *testing.T) {
	ctx := context.Background()
	h := newHost()
	c, _ := newClient(t, h)
	for _, n := range []string{"claude-w1", "claude-w2"} {
		if _, err := c.CreateServer(ctx, groupSpec(n, "dev")); err != nil {
			t.Fatal(err)
		}
	}
	h.containers["claude-w1"].run = false
	h.containers["claude-w2"].run = false
	before := h.called("up -d")
	if err := c.EnsureRunning(ctx, "claude-w1"); err != nil {
		t.Fatal(err)
	}
	if h.called("up -d") != before+1 || !h.containers["claude-w1"].run || h.containers["claude-w2"].run {
		t.Errorf("w1 %v w2 %v calls %v", h.containers["claude-w1"].run, h.containers["claude-w2"].run, h.calls)
	}
	var up string
	for _, call := range h.calls {
		if strings.Contains(call, "up -d") {
			up = call
		}
	}
	if !strings.HasSuffix(up, "claude-w1 claude-w1-docker") {
		t.Errorf("the services of w1 only: %q", up)
	}
}

// The machines of a group start at the same time: the file is changed one at a time, none is lost.
func TestMachinesOfAGroupStartedTogetherAreAllInTheFile(t *testing.T) {
	ctx := context.Background()
	h := newHost()
	c, dir := newClient(t, h)
	var wg sync.WaitGroup
	names := []string{"claude-w1", "claude-w2", "claude-w3", "claude-w4", "claude-w5"}
	errs := make([]error, len(names))
	for i, n := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = c.CreateServer(ctx, groupSpec(n, "dev"))
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("%s: %v", names[i], err)
		}
	}
	text := fileOf(t, dir, "g_dev")
	for _, n := range names {
		if !strings.Contains(text, "container_name: "+n+"\n") {
			t.Errorf("%s is not in the file", n)
		}
		if !exists(filepath.Join(dir, "compose", "g_dev", "secret", n, "host_key")) {
			t.Errorf("no keys of %s", n)
		}
	}
}

// What a machine of a group left is its keys and its part of the file; the others in the directory are no leftovers.
func TestLeftoversOfAMachineOfAGroup(t *testing.T) {
	ctx := context.Background()
	h := newHost()
	c, dir := newClient(t, h)
	for _, n := range []string{"claude-w1", "claude-w2"} {
		if _, err := c.CreateServer(ctx, groupSpec(n, "dev")); err != nil {
			t.Fatal(err)
		}
	}
	if l, err := c.Leftovers(ctx, "claude-w2"); err != nil || len(l) != 0 {
		t.Fatalf("a machine that exists has none: %v %v", l, err)
	}
	delete(h.containers, "claude-w1") // removed by hand
	l, err := c.Leftovers(ctx, "claude-w1")
	if err != nil || len(l) != 4 || !strings.Contains(strings.Join(l, " "), filepath.Join("secret", "claude-w1")) {
		t.Fatalf("%v %v", l, err)
	}
	if l, err := c.Leftovers(ctx, "claude-w2"); err != nil || len(l) != 0 {
		t.Fatalf("the other machine has none: %v %v", l, err)
	}
	if err := c.DeleteLeftovers(ctx, "claude-w1"); err != nil {
		t.Fatal(err)
	}
	if h.containers["claude-w2"] == nil || !exists(filepath.Join(dir, "compose", "g_dev", "secret", "claude-w2", "host_key")) {
		t.Error("the other machine was touched")
	}
	if strings.Contains(fileOf(t, dir, "g_dev"), "claude-w1") {
		t.Error("the file still has the machine")
	}
}

// A group name goes into paths: it is checked like a machine name.
func TestAGroupNameThatIsNoNameIsRefused(t *testing.T) {
	c, _ := newClient(t, newHost())
	for _, g := range []string{"../x", "a_b", "A", "a/b"} {
		if _, err := c.CreateServer(context.Background(), groupSpec("claude-w1", g)); err == nil {
			t.Errorf("group %q was accepted", g)
		}
	}
}

// The state directory may have characters that a pattern takes for its own ([ ] * ?): the last machine is found anyway.
func TestAStateDirectoryWithPatternCharactersIsNoProblem(t *testing.T) {
	ctx := context.Background()
	h := newHost()
	state := filepath.Join(t.TempDir(), "a[b]*")
	c, _ := newClient(t, h, func(o *compose.Options) { o.StateDir = state })
	var ids []string
	for _, n := range []string{"claude-w1", "claude-w2"} {
		s, err := c.CreateServer(ctx, groupSpec(n, "dev"))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, s.ID)
	}
	if err := c.DeleteServer(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(state, "compose", "g_dev", "secret", "claude-w2", "host_key")) || h.containers["claude-w2"] == nil {
		t.Error("the keys of the machine that is left were removed")
	}
	if p, err := c.Leftovers(ctx, "claude-w2"); err != nil || len(p) != 0 {
		t.Errorf("%v %v", p, err)
	}
}

// The files of a machine of an older version (a project of its own) do not stay when the machine is made again in a group.
func TestTheOldFilesOfAMachineAreDroppedWhenItIsMadeInAGroup(t *testing.T) {
	ctx := context.Background()
	h := newHost()
	c, dir := newClient(t, h)
	if _, err := c.CreateServer(ctx, spec("claude-w1")); err != nil {
		t.Fatal(err)
	}
	delete(h.containers, "claude-w1")
	delete(h.containers, "claude-w1-docker")
	if _, err := c.CreateServer(ctx, groupSpec("claude-w1", "dev")); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dir, "compose", "claude-w1")) {
		t.Error("the directory of the old project stays")
	}
	if !exists(filepath.Join(dir, "compose", "g_dev", "compose.yaml")) {
		t.Error("no file of the group")
	}
}
