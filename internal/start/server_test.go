package start

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/config"
	"github.com/pt9912/hetzner-viper/internal/servertest"
)

// These tests run the scripts that go to the server for real, on a fake server (servertest):
// in a sandbox home, with fake brew, claude, gh, swapon ... in front of the real programs. They
// are the Go version of what the Bash tests checked with the fake ssh.

type srvWorld struct {
	*world
	srv *servertest.Server
}

func newSrvWorld(t *testing.T, settings string) *srvWorld {
	t.Helper()
	brew := filepath.Join(t.TempDir(), "linuxbrew", "bin", "brew")
	// the work directory is a temporary one, as /Development cannot be made here
	w := newWorld(t, "VIPER_BREW="+brew+"\nWORKSPACE_DIR="+filepath.Join(t.TempDir(), "ws")+"\n"+settings)
	return &srvWorld{world: w, srv: servertest.New(t, brew)}
}

func (s *srvWorld) deps() Deps {
	d := s.world.deps()
	d.Run, d.Stream = s.srv.Run, s.srv.Stream
	// the fake server reads the certificate of the user-data (ssh-keyscan)
	return d
}

func (s *srvWorld) run(repo string) (int, error) {
	s.t.Helper()
	code, err := Run(context.Background(), s.c, repo, s.deps(), s.lg, &s.out, &s.err)
	s.lg.Close(err, code)
	s.srv.UserData = s.userData
	return code, err
}

// again is a second run on the server the first one made.
func (s *srvWorld) again(repo string) (int, error) {
	s.t.Helper()
	srv := s.createdServer
	srv.Created = s.now.Add(-time.Hour)
	s.server = &srv
	s.out.Reset()
	s.err.Reset()
	return s.run(repo)
}

func (s *srvWorld) mustRun(repo string) {
	s.t.Helper()
	if code, err := s.run(repo); code != 0 || err != nil {
		s.t.Fatalf("%d %v\n%s", code, err, s.all())
	}
}

func match(t *testing.T, re, s string) {
	t.Helper()
	if !regexp.MustCompile(re).MatchString(s) {
		t.Errorf("no match for %q in\n%s", re, s)
	}
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

// --- swap

func TestSwapIsOffByDefaultOnTheServer(t *testing.T) {
	w := newSrvWorld(t, "")
	w.mustRun("")
	if w.srv.Exists("swap.log") {
		t.Error("swap was touched")
	}
}

func TestSwapFactorOfTheRamAndIdempotence(t *testing.T) {
	w := newSrvWorld(t, "VIPER_SWAP=1\n")
	w.mustRun("")
	log := w.srv.File("swap.log")
	if !strings.Contains(log, "fallocate -l 8192M ") {
		t.Errorf("as large as the RAM (8 GB):\n%s", log)
	}
	if m := mode(t, filepath.Join(w.srv.State, "remote-swapfile")); m != 0o600 {
		t.Errorf("swap file mode %v", m)
	}
	match(t, `(?m)^mkswap`, log)
	match(t, `(?m)^swapon .*remote-swapfile`, log)
	if !strings.Contains(w.srv.File("remote-fstab"), "remote-swapfile none swap sw 0 0") {
		t.Error("not kept over reboots")
	}
	if !strings.Contains(w.srv.File("remote-sysctl.d/99-viper-swap.conf"), "vm.swappiness=10") {
		t.Error("swappiness")
	}
	match(t, `(?m)^swap: 8192 MB at .*RAM about 8 GB, VIPER_SWAP=1`, w.out.String())
	w.srv.Write("swap.log", "")
	w.c = reload(t, w, "VIPER_BREW="+w.srv.BrewPath+"\nWORKSPACE_DIR="+w.c.WorkspaceDir+"\nVIPER_SWAP=0.5\n")
	if code, err := w.again(""); code != 0 || err != nil {
		t.Fatalf("%d %v", code, err)
	}
	if strings.Contains(w.srv.File("swap.log"), "fallocate") {
		t.Error("not idempotent")
	}
	match(t, `(?m)^swap: .* already active`, w.out.String())
	if n := strings.Count(w.srv.File("remote-fstab"), "remote-swapfile"); n != 1 {
		t.Errorf("fstab has it %d times", n)
	}
}

func TestSwapFactorHalfAndSize(t *testing.T) {
	w := newSrvWorld(t, "VIPER_SWAP=0.5\n")
	w.mustRun("")
	if !strings.Contains(w.srv.File("swap.log"), "fallocate -l 4096M ") {
		t.Errorf("half:\n%s", w.srv.File("swap.log"))
	}
	w2 := newSrvWorld(t, "VIPER_SWAP=3G\n")
	w2.mustRun("")
	if !strings.Contains(w2.srv.File("swap.log"), "fallocate -l 3072M ") {
		t.Errorf("size:\n%s", w2.srv.File("swap.log"))
	}
}

func TestSwapFailureIsNotFatal(t *testing.T) {
	w := newSrvWorld(t, "VIPER_SWAP=1\n")
	w.srv.Env["MOCK_SWAP_FAIL"] = "1"
	w.mustRun("")
	has(t, w.err.String(), "WARNING: swap could not be set up")
	match(t, `(?m)^Ready: ssh hetzner-claude-web`, w.out.String())
	if w.srv.Exists("remote-swapfile") {
		t.Error("half-made file left behind")
	}
}

func TestSwapNeedsRoomOnTheDisk(t *testing.T) {
	w := newSrvWorld(t, "VIPER_SWAP=1\n")
	w.srv.Env["MOCK_DISK_FREE"] = "9000"
	w.mustRun("")
	has(t, w.out.String(), "swap: only 9000 MB free on the disk, 8192 MB needed")
	has(t, w.err.String(), "WARNING: swap could not be set up")
	if w.srv.Exists("remote-swapfile") {
		t.Error("created without room")
	}
}

// reload reads the settings again, as another run with changed settings would.
func reload(t *testing.T, w *srvWorld, settings string) *config.Config {
	t.Helper()
	dir := w.c.Dir
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\n"+settings), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(dir, "web", config.Env{Home: filepath.Dir(w.c.SSHDir), Lookup: func(string) (string, bool) { return "", false }}, false)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
