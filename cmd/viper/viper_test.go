package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func mustParse(t *testing.T, args ...string) (string, options) {
	t.Helper()
	name, o, err := parse(args)
	if err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return name, o
}

func scriptArgs(name string, o options) []string { return commands[name].args(&o) }

func TestScriptsRunInTheContainer(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"setup"}, []string{"./setup.sh"}},
		{[]string{"preflight", "web"}, []string{"./preflight.sh", "web", ""}},
		{[]string{"up", "web", "--repo", "https://x.org/a b.git?x=1&y=2;z"}, []string{"./start.sh", "web", "https://x.org/a b.git?x=1&y=2;z"}},
		{[]string{"check", "web"}, []string{"./check.sh", "web"}},
		{[]string{"ssh", "web"}, []string{"./connect.sh", "web"}},
		{[]string{"ssh", "web", "--cmd", `echo "$HOME" rc=$? $(id -u)`}, []string{"./connect.sh", "web", `echo "$HOME" rc=$? $(id -u)`}},
		{[]string{"down", "web"}, []string{"./stop.sh", "web"}},
		{[]string{"down", "--force", "web"}, []string{"./stop.sh", "web", "--force"}},
		{[]string{"up", "--repo=https://x.org/r$b.git", "web"}, []string{"./start.sh", "web", "https://x.org/r$b.git"}},
	}
	for _, c := range cases {
		name, o := mustParse(t, c.args...)
		if got := scriptArgs(name, o); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v: got %q, want %q", c.args, got, c.want)
		}
	}
}

func TestMachineIsRequiredWhereTheScriptsNeedOne(t *testing.T) {
	for _, n := range []string{"preflight", "up", "check", "ssh", "down", "dns"} {
		if _, _, err := parse([]string{n}); err == nil || !strings.Contains(err.Error(), "Usage: viper "+n+" <machine>") {
			t.Errorf("%s: want a usage error, got %v", n, err)
		}
	}
	for _, n := range []string{"setup", "log", "version"} {
		if _, _, err := parse([]string{n}); err != nil {
			t.Errorf("%s: %v", n, err)
		}
	}
}

func TestUnknownOrMisplacedOptionsAreRejected(t *testing.T) {
	for _, args := range [][]string{
		{"nope"}, {"up", "web", "--bogus"}, {"check", "web", "--repo", "x"}, {"up", "web", "--force"},
		{"up", "web", "--repo"}, {"down", "web", "--force=1"}, {"setup", "web"}, {"up", "a", "b"},
	} {
		if _, _, err := parse(args); err == nil {
			t.Errorf("%v: want an error", args)
		}
	}
	if _, _, err := parse([]string{"up", "-h"}); !errors.Is(err, errHelp) {
		t.Errorf("-h: %v", err)
	}
}

func TestDnsAndLogUseBashInTheContainer(t *testing.T) {
	name, o := mustParse(t, "dns", "web")
	got := scriptArgs(name, o)
	if got[0] != "-c" || got[1] != dnsScript || !slices.Equal(got[2:], []string{"_", "web"}) {
		t.Errorf("dns: %q", got)
	}
	name, o = mustParse(t, "log", "web", "--full")
	got = commands[name].args(&o)
	if got[0] != "-c" || got[1] != logScript || got[2] != "_" {
		t.Errorf("log: %q", got)
	}
	if !slices.Equal(o.extraEnv, []string{"MACHINE=web", "FULL=1"}) {
		t.Errorf("log env: %q", o.extraEnv)
	}
}

func testHost() host {
	return host{uid: "1000", gid: "1001", home: "/home/me", dir: "/work/viper", passwd: "/home/me/.local/state/hetzner-claude/passwd.1"}
}

func TestRunArgsMatchTheMakefile(t *testing.T) {
	a := runArgs(testHost(), "img:1", nil, []string{"./start.sh", "web", "u"})
	joined := strings.Join(a, "\n")
	for _, want := range []string{
		"run\n--rm\n-i\n--user\n1000:1001\n-e\nHOME=/home/me\n",
		"-v\n/home/me/.local/state/hetzner-claude/passwd.1:/etc/passwd:ro\n",
		"-v\n/home/me/.ssh:/home/me/.ssh\n",
		"-v\n/home/me/.local/state/hetzner-claude:/home/me/.local/state/hetzner-claude\n",
		"-v\n/work/viper:/repo:ro\nimg:1\n./start.sh\nweb\nu",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "\n-t\n") {
		t.Error("no tty expected")
	}
	h := testHost()
	h.tty, h.sshConfigMount = true, "/dots/ssh_config"
	joined = strings.Join(runArgs(h, "img:1", []string{"MACHINE=web"}, nil), "\n")
	for _, want := range []string{"run\n--rm\n-i\n-t\n", "-v\n/dots/ssh_config:/dots/ssh_config\n", "-e\nMACHINE=web\n"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestPassedVariablesAreTheOnesOfTheMakefile(t *testing.T) {
	mk, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	var inMake []string
	for _, line := range strings.Split(string(mk), "\n") {
		if strings.Contains(line, "-e HCLOUD_TOKEN") {
			for _, m := range regexp.MustCompile(`-e ([A-Z_]+)\b`).FindAllStringSubmatch(line, -1) {
				inMake = append(inMake, m[1])
			}
		}
	}
	if !slices.Equal(inMake, passEnv) {
		t.Errorf("Makefile passes %q, viper %q", inMake, passEnv)
	}
}

func TestPasswdHasTheHostUser(t *testing.T) {
	got, err := passwdContent("501", "20", "/Users/me")
	if err != nil || got != "root:x:0:0:root:/root:/bin/bash\nrunner:x:501:20::/Users/me:/bin/bash\n" {
		t.Errorf("%q %v", got, err)
	}
	if _, err := passwdContent("1", "1", "/home/a:b"); err == nil {
		t.Error("a home with ':' would corrupt the entry")
	}
}

func TestTimezone(t *testing.T) {
	link := func(string) (string, error) { return "/var/db/timezone/zoneinfo/Europe/Berlin", nil }
	nolink := func(string) (string, error) { return "", errors.New("not a link") }
	yes, no := func(string) bool { return true }, func(string) bool { return false }
	cases := []struct {
		name, tz string
		readlink func(string) (string, error)
		exists   func(string) bool
		goos     string
		want     []string
	}{
		{"TZ wins", "Asia/Tokyo", link, yes, "linux", []string{"-e", "TZ=Asia/Tokyo"}},
		{"name from the link (macOS)", "", link, yes, "darwin", []string{"-e", "TZ=Europe/Berlin"}},
		{"mounted file (Linux, no link)", "", nolink, yes, "linux", []string{"-v", "/etc/localtime:/etc/host-localtime:ro", "-e", "TZ=:/etc/host-localtime"}},
		{"nothing known", "", nolink, no, "linux", nil},
		{"file of a VM is not the host's", "", nolink, yes, "darwin", nil},
	}
	for _, c := range cases {
		if got := timezoneArgs(c.tz, c.readlink, c.exists, c.goos); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSshConfigTarget(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(home, ".ssh", "config")
	if got := sshConfigTarget(home); got != "" {
		t.Errorf("no config: %q", got)
	}
	os.WriteFile(cfg, []byte("Host x\n"), 0o600)
	if got := sshConfigTarget(home); got != "" {
		t.Errorf("plain file: %q", got)
	}
	dots := t.TempDir()
	os.WriteFile(filepath.Join(dots, "ssh_config"), []byte("Host x\n"), 0o600)
	os.Remove(cfg)
	os.Symlink(filepath.Join(dots, "ssh_config"), cfg)
	want, _ := filepath.EvalSymlinks(filepath.Join(dots, "ssh_config"))
	if got := sshConfigTarget(home); got != want {
		t.Errorf("symlink out of ~/.ssh: got %q, want %q", got, want)
	}
}

// fakeDocker records the calls; missing: `image inspect` fails; pullFails: `pull` fails.
func fakeDocker(t *testing.T, missing, pullFails bool) *[][]string {
	t.Helper()
	var calls [][]string
	oldRun, oldQuiet := dockerRun, dockerQuiet
	t.Cleanup(func() { dockerRun, dockerQuiet = oldRun, oldQuiet })
	dockerQuiet = func(a ...string) error {
		calls = append(calls, a)
		if missing {
			return errors.New("no such image")
		}
		return nil
	}
	dockerRun = func(a ...string) error {
		calls = append(calls, a)
		if pullFails && a[0] == "pull" {
			return errors.New("denied")
		}
		return nil
	}
	return &calls
}

func withVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

func TestReleaseBuildPullsItsImageOnce(t *testing.T) {
	withVersion(t, "v1.2.3")
	calls := fakeDocker(t, true, false)
	img, err := ensureImage(options{})
	if err != nil || img != "ghcr.io/pt9912/hetzner-viper:v1.2.3" {
		t.Fatalf("%q %v", img, err)
	}
	if !reflect.DeepEqual(*calls, [][]string{{"image", "inspect", img}, {"pull", img}}) {
		t.Errorf("calls %q", *calls)
	}
	calls = fakeDocker(t, false, false)
	if _, err := ensureImage(options{}); err != nil || len(*calls) != 1 {
		t.Errorf("an image that is there is not pulled again: %q %v", *calls, err)
	}
}

func TestPullFailureSaysHowToBuildLocally(t *testing.T) {
	withVersion(t, "v1.2.3")
	fakeDocker(t, true, true)
	if _, err := ensureImage(options{}); err == nil || !strings.Contains(err.Error(), "--local") {
		t.Errorf("got %v", err)
	}
}

func TestDevelopmentBuildAndLocalBuildTheImage(t *testing.T) {
	old := hubReachable
	t.Cleanup(func() { hubReachable = old })
	for _, c := range []struct {
		version string
		o       options
		hub     bool
		want    []string
	}{
		{"dev", options{dir: "/w"}, true, []string{"build", "--target", "run", "-t", "hetzner-claude-run", "/w"}},
		{"v1.2.3", options{local: true, dir: "/w"}, true, []string{"build", "--target", "run", "-t", "hetzner-claude-run", "/w"}},
		{"dev", options{dir: "/w"}, false, []string{"build", "--target", "run", "--build-arg", "BASE_IMAGE=mirror.gcr.io/library/debian:bookworm-slim", "-t", "hetzner-claude-run", "/w"}},
		{"dev", options{dir: "/w", baseImage: "r.example/d:12"}, false, []string{"build", "--target", "run", "--build-arg", "BASE_IMAGE=r.example/d:12", "-t", "hetzner-claude-run", "/w"}},
	} {
		withVersion(t, c.version)
		hubReachable = func() bool { return c.hub }
		calls := fakeDocker(t, false, false)
		img, err := ensureImage(c.o)
		if err != nil || img != "hetzner-claude-run" || !reflect.DeepEqual(*calls, [][]string{c.want}) {
			t.Errorf("%+v: %q %v %q", c, img, err, *calls)
		}
	}
}

func TestExplicitImageIsRunNotBuilt(t *testing.T) {
	withVersion(t, "dev")
	calls := fakeDocker(t, false, false)
	img, err := ensureImage(options{image: "my/img:1"})
	if err != nil || img != "my/img:1" || len(*calls) != 1 {
		t.Errorf("%q %v %q", img, err, *calls)
	}
}

func TestExecuteMountsAPasswdFileInTheStateDirectoryAndRemovesIt(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	os.WriteFile(filepath.Join(dir, "setup.sh"), nil, 0o755)
	withVersion(t, "v1.2.3")
	calls := fakeDocker(t, false, false)
	var passwd string
	dockerRun = func(a ...string) error {
		*calls = append(*calls, a)
		for _, x := range a {
			if strings.HasSuffix(x, ":/etc/passwd:ro") {
				passwd = strings.TrimSuffix(x, ":/etc/passwd:ro")
			}
		}
		if b, err := os.ReadFile(passwd); err != nil || !strings.Contains(string(b), "runner:x:") || !strings.Contains(string(b), home) {
			t.Errorf("passwd during the run: %q %v", b, err)
		}
		return nil
	}
	name, o := mustParse(t, "setup", "--dir", dir)
	if err := execute(commands[name], o); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(home, ".local", "state", "hetzner-claude")
	if filepath.Dir(passwd) != state {
		t.Errorf("passwd file %q is not in %q", passwd, state)
	}
	if _, err := os.Stat(passwd); !os.IsNotExist(err) {
		t.Error("passwd file left behind")
	}
}

func TestNoPasswdFileIsCreatedWhenTheImageCannotBeGot(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	os.WriteFile(filepath.Join(dir, "setup.sh"), nil, 0o755)
	withVersion(t, "v1.2.3")
	fakeDocker(t, true, true)
	name, o := mustParse(t, "setup", "--dir", dir)
	if err := execute(commands[name], o); err == nil {
		t.Fatal("want the pull error")
	}
	left, _ := filepath.Glob(filepath.Join(home, ".local", "state", "hetzner-claude", "passwd.*"))
	if len(left) != 0 {
		t.Errorf("left behind: %q", left)
	}
}

func TestNotACheckoutIsReportedBeforeAnythingRuns(t *testing.T) {
	calls := fakeDocker(t, false, false)
	name, o := mustParse(t, "setup", "--dir", t.TempDir())
	if err := execute(commands[name], o); err == nil || !strings.Contains(err.Error(), "not a hetzner-viper checkout") {
		t.Errorf("got %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("docker called: %q", *calls)
	}
}
