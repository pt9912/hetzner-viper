package start_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/usecase/start"
)

// runScript runs one of the scripts of the MCP step in bash, like the machine does.
func runScript(t *testing.T, env []string, stdin, script string, args ...string) (string, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the scripts run on the server, in bash")
	}
	cmd := exec.Command("bash", append([]string{"-c", script, "nats"}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	b, err := cmd.CombinedOutput()
	return string(b), err
}

func sum(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func TestTheMCPBinaryIsPlacedOnlyIfItsChecksumFits(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nats")
	prog := "#!/bin/sh\necho viper-nats-mcp test\n"
	out, err := runScript(t, nil, prog, start.NatsMCPInstallScript(), dir, sum(prog))
	if err != nil || !strings.Contains(out, "viper-nats-mcp test installed") {
		t.Fatalf("%v\n%s", err, out)
	}
	if fi, err := os.Stat(filepath.Join(dir, "viper-nats-mcp")); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("binary: %v %v", fi, err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("directory %v", fi.Mode())
	}
	// a binary that was damaged on the way does not replace the good one
	out, err = runScript(t, nil, "#!/bin/sh\necho evil\n", start.NatsMCPInstallScript(), dir, sum(prog))
	if err == nil || !strings.Contains(out, "checksum") {
		t.Fatalf("must refuse: %v\n%s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "viper-nats-mcp")); string(b) != prog {
		t.Error("the installed binary was changed")
	}
	if _, err := os.Stat(filepath.Join(dir, "viper-nats-mcp.new")); err == nil {
		t.Error("the refused file is left behind")
	}
}

// fakeClaude puts a claude that records its calls, and the real jq, on the path of a fresh home.
func fakeClaude(t *testing.T) (env []string, home string) {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("no jq")
	}
	home = t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"$@\" >> \"$HOME/claude.calls\"\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return []string{"HOME=" + home}, home
}

func TestClaudeCodeGetsTheServerAndExactlyTheAllowedTools(t *testing.T) {
	env, home := fakeClaude(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"model":"x","permissions":{"allow":["Bash(ls)","mcp__nats__send","mcp__other__tool"],"deny":["Bash(rm)"]}}`
	if err := os.WriteFile(settings, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runScript(t, env, "", start.NatsMCPClaudeScript(), "/home/builder/.viper-nats/viper-nats-mcp", "peers,inbox")
	if err != nil || !strings.Contains(out, "registered with Claude Code") || !strings.Contains(out, "allowed without asking: peers,inbox") {
		t.Fatalf("%v\n%s", err, out)
	}
	calls, _ := os.ReadFile(filepath.Join(home, "claude.calls"))
	if !strings.Contains(string(calls), "mcp remove --scope user nats") || !strings.Contains(string(calls), "mcp add --scope user nats -- /home/builder/.viper-nats/viper-nats-mcp") {
		t.Errorf("claude calls:\n%s", calls)
	}
	got, _ := os.ReadFile(settings)
	want := `{"model":"x","permissions":{"allow":["Bash(ls)","mcp__other__tool","mcp__nats__peers","mcp__nats__inbox"],"deny":["Bash(rm)"]}}`
	if compact(t, string(got)) != want {
		t.Errorf("settings:\n%s", got)
	}
	// off: the rights given earlier are taken away again, the rest stays
	if out, err := runScript(t, env, "", start.NatsMCPClaudeScript(), "/b", ""); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got, _ = os.ReadFile(settings)
	if strings.Contains(string(got), "mcp__nats__") || !strings.Contains(string(got), "mcp__other__tool") || !strings.Contains(string(got), "Bash(rm)") {
		t.Errorf("after off:\n%s", got)
	}
}

func compact(t *testing.T, s string) string {
	t.Helper()
	cmd := exec.Command("jq", "-c", ".")
	cmd.Stdin = strings.NewReader(s)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestWithoutAnAllowListNoSettingsFileIsMade(t *testing.T) {
	env, home := fakeClaude(t)
	if out, err := runScript(t, env, "", start.NatsMCPClaudeScript(), "/b", ""); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); err == nil {
		t.Error("nothing to allow, nothing to write")
	}
}

func TestWithoutClaudeCodeTheRegistrationSaysSo(t *testing.T) {
	home := t.TempDir()
	out, err := runScript(t, []string{"HOME=" + home, "PATH=/usr/bin:/bin"}, "", start.NatsMCPClaudeScript(), "/b", "")
	if err == nil || !strings.Contains(out, "Claude Code is not installed") {
		t.Skipf("a claude in /usr/bin on this host: %v\n%s", err, out)
	}
}

func TestTheConnectionTestNeedsTheToolsOfTheServer(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	bad := filepath.Join(dir, "bad")
	if err := os.WriteFile(good, []byte("#!/bin/sh\ncat >/dev/null\necho '{\"result\":{\"tools\":[{\"name\":\"peers\"},{\"name\":\"send\"}]}}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("#!/bin/sh\necho 'viper-nats-mcp: connection.env: no such file'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := runScript(t, nil, "", start.NatsMCPTestScript(), good); err != nil || !strings.Contains(out, "offers its tools") {
		t.Errorf("%v\n%s", err, out)
	}
	if out, err := runScript(t, nil, "", start.NatsMCPTestScript(), bad); err == nil || !strings.Contains(out, "connection.env") {
		t.Errorf("must fail and show why: %v\n%s", err, out)
	}
}

func TestOnlyALinuxProgramOfTheRightArchitectureIsAcceptedFromNATSMCPBinary(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the test program must be an ELF file")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	here, other := runtime.GOARCH, "arm64"
	if here == "arm64" {
		other = "amd64"
	}
	if here != "amd64" && here != "arm64" {
		t.Skip("neither amd64 nor arm64")
	}
	if err := start.CheckLinuxBinary(b, here); err != nil {
		t.Errorf("the right one: %v", err)
	}
	if err := start.CheckLinuxBinary(b, other); err == nil {
		t.Error("the wrong architecture must be refused")
	}
	if err := start.CheckLinuxBinary([]byte("#!/bin/sh\necho hi\n"), here); err == nil {
		t.Error("a script is no program for the machine")
	}
}

func TestABinaryThatDoesNotRunDoesNotReplaceTheOneThatWorks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nats")
	good := "#!/bin/sh\necho viper-nats-mcp good\n"
	if out, err := runScript(t, nil, good, start.NatsMCPInstallScript(), dir, sum(good)); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	broken := "#!/bin/sh\nexit 1\n"
	out, err := runScript(t, nil, broken, start.NatsMCPInstallScript(), dir, sum(broken))
	if err == nil || !strings.Contains(out, "does not run") {
		t.Fatalf("must refuse: %v\n%s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "viper-nats-mcp")); string(b) != good {
		t.Error("the working binary was replaced")
	}
}

func TestSettingsOfClaudeCodeStayAFileLinkedIntoADotfilesRepository(t *testing.T) {
	env, home := fakeClaude(t)
	target := filepath.Join(home, "dotfiles", "settings.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{"permissions":{"allow":["Bash(ls)"]}}`), 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skip("no symlinks here")
	}
	if out, err := runScript(t, env, "", start.NatsMCPClaudeScript(), "/b", "peers"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link was replaced by a file: %v", err)
	}
	got, _ := os.ReadFile(target)
	if !strings.Contains(string(got), "mcp__nats__peers") || !strings.Contains(string(got), "Bash(ls)") {
		t.Errorf("%s", got)
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o640 {
		t.Errorf("the rights of the file changed: %v", fi.Mode())
	}
	left, _ := filepath.Glob(target + ".*")
	if len(left) != 0 {
		t.Errorf("temporary files are left: %v", left)
	}
}
