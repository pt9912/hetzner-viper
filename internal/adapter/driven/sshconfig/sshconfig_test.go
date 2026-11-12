package sshconfig_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driven/settingsfile"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/sshconfig"
	"github.com/pt9912/hetzner-viper/internal/model"
	portsettings "github.com/pt9912/hetzner-viper/internal/port/driven/settings"
)

func files() sshconfig.Files { return sshconfig.Files{} }

func testConfig(t *testing.T) *model.Machine {
	t.Helper()
	home, dir := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("DNS_DOMAIN=example.com\nWORKSPACE_DIR=/Development\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := settingsfile.Load(dir, "web", portsettings.Env{Home: home, Lookup: func(string) (string, bool) { return "", false }}, false)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSSHConfigPinsTheHostKeyOfThisServer(t *testing.T) {
	c := testConfig(t)
	if err := files().WriteConfig(c, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(c.SSHConfig)
	for _, want := range []string{
		"Host hetzner-claude-web\n", "    HostName 203.0.113.7\n", "    HostKeyAlias claude-web.example.com\n", "    User builder\n",
		"    IdentityFile " + c.SSHKey + "\n", "    StrictHostKeyChecking yes\n", "    CheckHostIP no\n",
		"    UserKnownHostsFile " + filepath.Join(c.SSHDir, "known_hosts_hetzner_claude_ca") + "\n", // no pin yet: host CA
		"    HostKeyAlgorithms ssh-ed25519-cert-v01@openssh.com,ssh-ed25519\n", "    ForwardAgent no\n", "    ForwardX11 no\n",
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q in\n%s", want, b)
		}
	}
	if st, _ := os.Stat(c.SSHConfig); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	if err := os.WriteFile(c.KnownPin, []byte("claude-web.example.com ssh-ed25519 AAAA\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := files().WriteConfig(c, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(c.SSHConfig)
	if !strings.Contains(string(b), "    UserKnownHostsFile "+c.KnownPin+"\n") {
		t.Errorf("a pinned key is used:\n%s", b)
	}
	// an empty pin file does not count
	if err := os.WriteFile(c.KnownPin, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := files().WriteConfig(c, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(c.SSHConfig)
	if strings.Contains(string(b), "UserKnownHostsFile "+c.KnownPin) {
		t.Errorf("empty pin used:\n%s", b)
	}
}

func TestForgetStalePin(t *testing.T) {
	c := testConfig(t)
	var out strings.Builder
	if err := os.MkdirAll(c.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.KnownPin, []byte("pin"), 0o600); err != nil {
		t.Fatal(err)
	}
	files().ForgetStalePin(c, "5", &out) // no recorded ID: keep
	if err := os.WriteFile(c.IDState, []byte("5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	files().ForgetStalePin(c, "5", &out) // same server: keep
	if _, err := os.Stat(c.KnownPin); err != nil || out.Len() != 0 {
		t.Fatalf("kept: %v %q", err, out.String())
	}
	files().ForgetStalePin(c, "6", &out)
	if _, err := os.Stat(c.KnownPin); err == nil || out.String() != "Server claude-web was recreated (ID 5 -> 6); dropping its old pinned host key.\n" {
		t.Errorf("dropped: %v %q", err, out.String())
	}
}

func TestPinFromServer(t *testing.T) {
	c := testConfig(t)
	if err := os.MkdirAll(c.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	var asked []string
	ssh := func(args ...string) (string, error) {
		asked = args
		return "ssh-ed25519 AAAAhostkey root@server\n", nil
	}
	files().PinHostKey(c, "7", ssh, &out)
	b, _ := os.ReadFile(c.KnownPin)
	if string(b) != "claude-web.example.com ssh-ed25519 AAAAhostkey\n" {
		t.Errorf("pin %q", b)
	}
	if id, _ := os.ReadFile(c.IDState); string(id) != "7\n" {
		t.Errorf("id %q", id)
	}
	if out.String() != "Pinned host key of claude-web (verified via host CA).\n" || asked[len(asked)-1] != "cat /etc/ssh/ssh_host_ed25519_key.pub" {
		t.Errorf("%q %q", out.String(), asked)
	}
	// an existing pin stays; garbage is ignored
	asked = nil
	files().PinHostKey(c, "8", ssh, &out)
	if asked != nil {
		t.Error("asked although pinned")
	}
	_ = os.Remove(c.KnownPin) // may not exist
	files().PinHostKey(c, "8", func(...string) (string, error) { return "garbage", nil }, &out)
	files().PinHostKey(c, "8", func(...string) (string, error) { return "", errors.New("x") }, &out)
	if _, err := os.Stat(c.KnownPin); err == nil {
		t.Error("pinned garbage")
	}
}

func TestSSHIncludeEditsInPlaceAndKeepsASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs symlinks and file modes")
	}
	c := testConfig(t)
	if err := os.MkdirAll(c.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dots := t.TempDir()
	target := filepath.Join(dots, "ssh_config")
	if err := os.WriteFile(target, []byte("Host other\n  User x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(c.SSHDir, "config")); err != nil {
		t.Fatal(err)
	}
	line := "Include " + c.SSHConfig + "\n"
	if err := files().AddInclude(c); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != line+"Host other\n  User x\n" {
		t.Errorf("added: %q", b)
	}
	if st, _ := os.Lstat(filepath.Join(c.SSHDir, "config")); st.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced")
	}
	if err := files().AddInclude(c); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); strings.Count(string(b), "Include ") != 1 {
		t.Errorf("added twice: %q", b)
	}
	if st, _ := os.Stat(target); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	if err := files().RemoveInclude(c); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != "Host other\n  User x\n" {
		t.Errorf("removed: %q", b)
	}
}

func TestSSHIncludeCreatesTheFileAndFailsOnAReadOnlyOne(t *testing.T) {
	c := testConfig(t)
	if err := os.MkdirAll(c.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := files().AddInclude(c); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(c.SSHDir, "config")); string(b) != "Include "+c.SSHConfig+"\n" {
		t.Errorf("%q", b)
	}
	if os.Getuid() == 0 {
		return
	}
	if err := os.Chmod(filepath.Join(c.SSHDir, "config"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := files().RemoveInclude(c); err == nil {
		t.Error("a read-only file cannot be edited")
	}
	if w := files().IncludeWarning(c); !strings.HasPrefix(w, "WARNING: could not edit ~/.ssh/config (") || !strings.Contains(w, "add or remove the line 'Include "+c.SSHConfig+"' yourself.") {
		t.Errorf("%q", w)
	}
}

func TestParallelIncludesDoNotLoseLines(t *testing.T) {
	c := testConfig(t)
	if err := os.MkdirAll(c.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.SSHDir, "config"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error)
	for i := 0; i < 8; i++ {
		go func(i int) {
			cc := *c
			cc.SSHConfig = filepath.Join(c.SSHDir, "config_m"+string(rune('a'+i)))
			done <- files().AddInclude(&cc)
		}(i)
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(c.SSHDir, "config"))
	if n := strings.Count(string(b), "Include "); n != 8 {
		t.Errorf("%d lines, want 8:\n%s", n, b)
	}
}

// Several runs of a group write the config of the server and read it with ssh -F at the same time:
// a reader must never see the file empty or cut off.
func TestWriteConfigIsAtomicForReadersThatRunAtTheSameTime(t *testing.T) {
	c := testConfig(t)
	if err := files().WriteConfig(c, "203.0.113.1"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	bad := make(chan string, 1)
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			time.Sleep(100 * time.Microsecond) // a reader that never lets go would keep Windows from replacing the file
			b, err := os.ReadFile(c.SSHConfig)
			if err != nil {
				continue // Windows refuses to open a file in the moment it is replaced; a half file is what matters
			}
			if !strings.HasPrefix(string(b), "Host ") || !strings.HasSuffix(string(b), "ForwardX11 no\n") {
				select {
				case bad <- string(b):
				default:
				}
				return
			}
		}
	}()
	for i := 0; i < 300; i++ {
		if err := files().WriteConfig(c, "203.0.113."+string(rune('1'+i%2))); err != nil {
			t.Fatal(err)
		}
	}
	close(done)
	select {
	case got := <-bad:
		t.Fatalf("a reader saw a file that was not whole: %q", got)
	default:
	}
	left, _ := filepath.Glob(c.SSHConfig + ".tmp*")
	if len(left) != 0 {
		t.Errorf("temporary files are left behind: %v", left)
	}
	st, err := os.Stat(c.SSHConfig)
	if err != nil || (runtime.GOOS != "windows" && st.Mode().Perm() != 0o600) {
		t.Errorf("mode %v %v", st, err)
	}
}

func TestWriteConfigKeepsALinkAndRemovesOldTemporaryFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links need rights on Windows")
	}
	c := testConfig(t)
	target := filepath.Join(t.TempDir(), "dotfiles-config")
	if err := os.WriteFile(target, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(c.SSHConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, c.SSHConfig); err != nil {
		t.Skip("no symlinks here")
	}
	// the leftover of a crash, an hour and more old
	stale := target + ".tmp123"
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, past, past); err != nil {
		t.Fatal(err)
	}
	if err := files().WriteConfig(c, "203.0.113.9"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(c.SSHConfig); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link was replaced: %v", err)
	}
	if b, _ := os.ReadFile(target); !strings.Contains(string(b), "203.0.113.9") {
		t.Errorf("the file the link points to has the new config: %q", b)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("the old temporary file is still there")
	}
}

func TestAComposeContainerIsReachedThroughDockerProxy(t *testing.T) {
	c := podConfig(t, "web")
	c.Provider = "compose"
	if err := files().WriteConfig(c, ""); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(c.SSHConfig)
	for _, want := range []string{"    Port 2222\n", "\" \"docker-proxy\" \"--dir\" \""} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q in\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "k8s-proxy") {
		t.Errorf("a compose container is not reached through the cluster proxy:\n%s", b)
	}
}

func podConfig(t *testing.T, name string) *model.Machine {
	t.Helper()
	home, dir := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("PROVIDER=kubernetes\nDNS_PROVIDER=none\nSERVER_LOCATION=k3d-x\nK8S_IMAGE=i\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := settingsfile.Load(dir, name, portsettings.Env{Home: home, Lookup: func(string) (string, bool) { return "", false }}, false)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAPodIsReachedThroughTheProxyOnPort2222(t *testing.T) {
	c := podConfig(t, "web")
	if err := files().WriteConfig(c, ""); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(c.SSHConfig)
	for _, want := range []string{"    HostName claude-web\n", "    Port 2222\n", "    ProxyCommand \"", "\" \"k8s-proxy\" \"--dir\" \"", "\" \"web\"\n", "    StrictHostKeyChecking yes\n", "    ServerAliveInterval 15\n"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q in\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "HostName \n") || strings.Contains(string(b), "HostName 203") {
		t.Errorf("a pod has no address:\n%s", b)
	}
	// the host key of a pod is pinned from its home directory, not from /etc/ssh
	var asked string
	files().PinHostKey(c, "id1", func(args ...string) (string, error) {
		asked = strings.Join(args, " ")
		return "ssh-ed25519 AAAA host\n", nil
	}, &strings.Builder{})
	if !strings.HasSuffix(asked, "cat /home/builder/.viper/sshd/host_key.pub") {
		t.Errorf("%s", asked)
	}
}

func TestTheProxyCommandCannotCarryAnythingIntoTheFile(t *testing.T) {
	c := podConfig(t, "web")
	c.Dir = c.Dir + "/a\"b"
	if err := files().WriteConfig(c, ""); err == nil || !strings.Contains(err.Error(), "ProxyCommand") {
		t.Errorf("a quote in the directory: %v", err)
	}
	c.Dir = c.Dir[:len(c.Dir)-4] + "/50%d"
	if err := files().WriteConfig(c, ""); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(c.SSHConfig)
	if !strings.Contains(string(b), "50%%d") || strings.Contains(string(b), "50%d\"") {
		t.Errorf("ssh expands a percent sign in a ProxyCommand: it is doubled\n%s", b)
	}
}

func TestTheProxyCommandRefusesWhatTheShellOfSshWouldRun(t *testing.T) {
	for _, bad := range []string{"/a$(id)b", "/a`id`b", "/a$HOMEb"} {
		c := podConfig(t, "web")
		c.Dir = c.Dir + bad
		if err := files().WriteConfig(c, ""); err == nil || !strings.Contains(err.Error(), "ProxyCommand") {
			t.Errorf("%q was accepted: %v", bad, err)
		}
	}
}
