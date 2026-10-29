// Package servertest is a stand-in for the Hetzner server in tests: ssh and scp calls are run
// on this computer, in a sandbox home, with fake programs (brew, claude, gh, swapon, ...) in front
// of the real ones. So the scripts that run on the server are really executed. It is the Go
// version of the fake ssh that the Bash tests used. Only tests import it.
package servertest

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

//go:embed remote/*
var mocks embed.FS

// Server is one fake server. The files it keeps (logs of the fake programs, the sandbox home) are
// under State; the fake programs read the MOCK_* variables from Env.
type Server struct {
	t *testing.T
	// State is where the fake programs keep their files ($MOCK_STATE).
	State string
	// Home is the home directory of builder on the server.
	Home string
	// BrewPath is where Homebrew's installer puts brew (the VIPER_BREW setting).
	BrewPath string
	// Env are MOCK_* variables for the fake programs, e.g. MOCK_SWAP_FAIL=1.
	Env map[string]string
	// UserData is the user-data the server was created with (ssh-keyscan reads its certificate).
	UserData string

	mu      sync.Mutex
	calls   []string
	mockDir string
}

// SkipOrFail ends a test that cannot run here because a tool is missing. With VIPER_REQUIRE_TOOLS=1
// (set in CI) that is a failure instead: a runner without the tools must not turn the main safety
// net into a pile of skipped tests.
func SkipOrFail(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("VIPER_REQUIRE_TOOLS") == "1" {
		t.Fatalf("a tool is missing, but VIPER_REQUIRE_TOOLS=1: "+format, args...)
	}
	t.Skipf(format, args...)
}

// New makes a fake server in a temporary directory.
func New(t *testing.T, brewPath string) *Server {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake server runs the server scripts in bash on Unix paths")
	}
	root := t.TempDir()
	s := &Server{t: t, State: filepath.Join(root, "state"), Home: filepath.Join(root, "state", "remote-home"), BrewPath: brewPath,
		Env: map[string]string{}, mockDir: filepath.Join(root, "remote")}
	for _, d := range []string{s.State, s.mockDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := mocks.ReadDir("remote")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, _ := mocks.ReadFile("remote/" + e.Name())
		if err := os.WriteFile(filepath.Join(s.mockDir, e.Name()), b, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// the sleeps of the scripts do not wait
	if err := os.WriteFile(filepath.Join(s.mockDir, "sleep"), []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return s
}

// SetHostKey sets the public host key the server reports (ssh_host_ed25519_key.pub).
func (s *Server) SetHostKey(pub string) {
	if err := os.WriteFile(filepath.Join(s.State, "remote-hostkey.pub"), []byte(pub), 0o644); err != nil {
		s.t.Error(err)
	}
}

// Calls are the ssh and scp calls so far, one per line, like the Bash mock's calls.log.
func (s *Server) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// File reads a file of the fake programs ("" if there is none), relative to State.
func (s *Server) File(name string) string {
	b, _ := os.ReadFile(filepath.Join(s.State, name))
	return string(b)
}

// Exists reports whether a file under State exists.
func (s *Server) Exists(name string) bool {
	_, err := os.Stat(filepath.Join(s.State, name))
	return err == nil
}

// mkdir creates a directory of the sandbox; a failure fails the test.
func (s *Server) mkdir(dir string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.t.Error(err)
	}
}

// Write puts a file under State (for tests that prepare the server).
func (s *Server) Write(name, content string) {
	p := filepath.Join(s.State, name)
	s.mkdir(filepath.Dir(p))
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

// Run is the deps function of the commands that capture output.
func (s *Server) Run(ctx context.Context, stdin, name string, args ...string) (string, string, error) {
	var out, errb bytes.Buffer
	err := s.exec(ctx, stdin, &out, &errb, name, args)
	return out.String(), errb.String(), err
}

// Stream is the deps function of the commands that show the output as it comes.
func (s *Server) Stream(ctx context.Context, stdin string, stdout, stderr io.Writer, name string, args ...string) error {
	return s.exec(ctx, stdin, stdout, stderr, name, args)
}

type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

func fail(stderr io.Writer, code int, msg string) error {
	if msg != "" {
		fmt.Fprintln(stderr, msg)
	}
	return exitError{code}
}

func (s *Server) log(line string) {
	s.mu.Lock()
	s.calls = append(s.calls, line)
	s.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(s.State, "calls.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		fmt.Fprintln(f, line)
		_ = f.Close() // the log is best effort
	}
}

func (s *Server) exec(ctx context.Context, stdin string, stdout, stderr io.Writer, name string, args []string) error {
	switch name {
	case "scp":
		s.log("scp " + strings.Join(args, " "))
		// the deploy key lands in State/scp/<name>
		s.mkdir(filepath.Join(s.State, "scp"))
		src, dst := args[len(args)-2], args[len(args)-1]
		b, err := os.ReadFile(src)
		if err != nil {
			return fail(stderr, 1, err.Error())
		}
		return os.WriteFile(filepath.Join(s.State, "scp", filepath.Base(dst)), b, 0o600)
	case "ssh-keyscan":
		return s.keyscan(args, stdout)
	case "ssh-keygen":
		// reading a certificate (ssh-keygen -L) is the real program
		cmd := exec.CommandContext(ctx, "ssh-keygen", args...)
		cmd.Stdout, cmd.Stderr = stdout, stderr
		return cmd.Run()
	case "ssh":
		return s.ssh(ctx, stdin, stdout, stderr, args)
	}
	return fail(stderr, 127, name+": not faked")
}

var certRe = regexp.MustCompile(`(?m)^\s*ed25519_certificate:\s*"([^"]*)"`)

func (s *Server) keyscan(_ []string, stdout io.Writer) error {
	if m := certRe.FindStringSubmatch(s.UserData); m != nil {
		// like the real ssh-keyscan -c: "<type> <base64>" without the comment
		f := strings.Fields(m[1])
		if len(f) >= 2 {
			fmt.Fprintln(stdout, f[0], f[1])
		}
	}
	return nil
}

func (s *Server) env() []string {
	e := append(os.Environ(),
		"HOME="+s.Home, "MOCK_STATE="+s.State, "VIPER_TMP_DIRS="+filepath.Join(s.State, "remote-tmp"),
		"VIPER_REBOOT_FILE="+filepath.Join(s.State, "reboot-required"), "VIPER_CLOUD_RESULT="+filepath.Join(s.State, "cloud-result.json"),
		"VIPER_SWAPFILE="+filepath.Join(s.State, "remote-swapfile"), "VIPER_FSTAB="+filepath.Join(s.State, "remote-fstab"),
		"VIPER_SYSCTL_D="+filepath.Join(s.State, "remote-sysctl.d"), "VIPER_MEMINFO="+filepath.Join(s.State, "meminfo"),
		"VIPER_BREW="+s.BrewPath, "PATH="+s.mockDir+":"+os.Getenv("PATH"), "GIT_CONFIG_GLOBAL="+filepath.Join(s.Home, ".gitconfig"))
	for k, v := range s.Env {
		e = append(e, k+"="+v)
	}
	return e
}

// bash runs a command string on the "server": in the sandbox home, with the fake programs.
func (s *Server) bash(ctx context.Context, remote, stdin string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, "bash", "-c", remote)
	cmd.Env = s.env()
	cmd.Dir = s.Home
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	return cmd.Run()
}

// ssh is the fake ssh: like the real one, everything after the host is joined into ONE string that
// the remote shell parses again.
func (s *Server) ssh(ctx context.Context, stdin string, stdout, stderr io.Writer, args []string) error {
	s.log("ssh " + strings.Join(args, " "))
	remote, noStdin := s.sshRemote(args)
	if noStdin {
		stdin = ""
	}
	if failed, err := s.sshFailure(args, stderr); failed {
		return err
	}
	s.prepareSandbox()
	return s.dispatch(ctx, remote, stdin, stdout, stderr)
}

// sshRemote splits the arguments of the fake ssh into the remote command and the -n flag.
func (s *Server) sshRemote(args []string) (remote string, noStdin bool) {
	i := 0
	for i < len(args) && !strings.HasPrefix(args[i], "hetzner-") {
		if args[i] == "-n" {
			noStdin = true
		}
		if args[i] == "-t" {
			s.log("ssh: terminal requested")
		}
		i++
	}
	if i+1 < len(args) {
		remote = strings.Join(args[i+1:], " ")
	}
	return remote, noStdin
}

func (s *Server) flag(k string) bool { return s.Env[k] != "" }

// sshFailure is the simulated connection failure (MOCK_SSH_*), if one is switched on.
func (s *Server) sshFailure(args []string, stderr io.Writer) (bool, error) {
	flag := s.flag
	switch {
	case flag("MOCK_SSH_DOWN"):
		return true, fail(stderr, 255, "ssh: connect to host 203.0.113.10 port 22: Connection timed out")
	case flag("MOCK_SSH_HOSTKEY_FAIL"):
		return true, fail(stderr, 255, "Host key verification failed.")
	case flag("MOCK_SSH_AUTH_FAIL"):
		return true, fail(stderr, 255, "builder@203.0.113.10: Permission denied (publickey).")
	}
	// connection like another device (only the host CA is trusted): MOCK_CA_FAIL = expired certificate
	if strings.Contains(strings.Join(args, " "), "known_hosts_hetzner_claude_ca") && flag("MOCK_CA_FAIL") {
		fmt.Fprintln(stderr, "Certificate invalid: expired")
		return true, fail(stderr, 255, "Host key verification failed.")
	}
	return false, nil
}

// prepareSandbox makes sure the directories and files the fake server needs exist.
func (s *Server) prepareSandbox() {
	s.mkdir(s.Home)
	s.mkdir(filepath.Join(s.State, "remote-tmp"))
	s.mkdir(filepath.Join(s.State, "remote-sysctl.d"))
	if !s.Exists("meminfo") {
		s.Write("meminfo", "MemTotal:        7800000 kB\n")
	}
}

// dispatch runs the remote command the way the matching case of the fake server does.
func (s *Server) dispatch(ctx context.Context, remote, stdin string, stdout, stderr io.Writer) error {
	flag := s.flag
	switch {
	// readiness probe: fake cloud-init/docker, sandboxed marker file
	case strings.HasPrefix(remote, "cloud-init status"):
		err := s.bash(ctx, strings.ReplaceAll(remote, "/var/tmp/claude-ready", filepath.Join(s.State, "claude-ready")), "", stdout, stderr)
		// the pending reboot happens right after this probe, unless the test keeps the flag
		if !s.Exists("reboot-required.keep") {
			_ = os.Remove(filepath.Join(s.State, "reboot-required")) // may not exist
		}
		return err
	// Homebrew's installer (MOCK_BREW_FAIL: it fails); must come before Claude's install.sh
	case strings.Contains(remote, "Homebrew/install"):
		return s.installBrew(stdout)
	// code-server's installer (MOCK_CODE_SERVER_FAIL: it fails); before Claude's install.sh
	case strings.Contains(remote, "code-server.dev/install.sh"):
		return s.installCodeServer(stdout)
	case strings.Contains(remote, "install.sh"):
		return s.installClaude(stderr)
	// Claude token login: run it for real in the sandbox home (stdin = token)
	case strings.Contains(remote, "viper/claude.env"):
		return s.bash(ctx, remote, stdin, stdout, stderr)
	// code-server config (stdin = config.yaml) and its systemd unit
	case strings.Contains(remote, "code-server/config.yaml"):
		return s.bash(ctx, remote, stdin, stdout, stderr)
	// creating the workspace (sudo install -d ...) and recording it
	case strings.HasPrefix(remote, "sudo install -d"):
		return s.bash(ctx, remote, "", stdout, stderr)
	case strings.HasPrefix(remote, "gh auth login"):
		if flag("MOCK_GH_LOGIN_FAIL") {
			return fail(stderr, 1, "error validating token")
		}
		s.Write("gh-token", stdin)
	// VIPER_POST_CLONE: the check for the marker of a fresh clone, and the commands (stdin) into a file
	case isPostCloneCall(remote):
		return s.bash(ctx, remote, stdin, stdout, stderr)
	// host key read over a CA-verified connection
	case remote == "cat /etc/ssh/ssh_host_ed25519_key.pub":
		fmt.Fprint(stdout, s.File("remote-hostkey.pub"))
	// MOCK_SSH_SKIP_SCRIPT: ssh succeeds but the remote script does not run (e.g. .bashrc exits)
	case strings.HasPrefix(remote, "bash -s"):
		if flag("MOCK_SSH_SKIP_SCRIPT") {
			return nil
		}
		return s.bash(ctx, remote, stdin, stdout, stderr)
	}
	return nil
}

// isPostCloneCall tells the two calls of VIPER_POST_CLONE that are not "bash -s": the check for
// the marker of a fresh clone and the writing of the commands.
func isPostCloneCall(remote string) bool {
	return strings.HasPrefix(remote, "test -e ") && strings.Contains(remote, "viper-post-clone-pending") ||
		strings.Contains(remote, "viper/post-clone.sh")
}

// installBrew is Homebrew's installer (MOCK_BREW_FAIL: it fails).
func (s *Server) installBrew(stdout io.Writer) error {
	flag := s.flag
	if flag("MOCK_BREW_FAIL") {
		fmt.Fprintln(stdout, "Error: Failed to download")
		return exitError{1}
	}
	// the installer puts brew where the scripts expect it
	if _, err := os.Stat(s.BrewPath); err != nil {
		s.mkdir(filepath.Dir(s.BrewPath))
		_ = os.Symlink(filepath.Join(s.mockDir, "brew"), s.BrewPath)
	}
	return nil
}

// installCodeServer is code-server's installer (MOCK_CODE_SERVER_FAIL: it fails).
func (s *Server) installCodeServer(stdout io.Writer) error {
	flag := s.flag
	if flag("MOCK_CODE_SERVER_FAIL") {
		fmt.Fprintln(stdout, "Error: no .deb for this system")
		return exitError{1}
	}
	s.Write("code-server-installed", "")
	return nil
}

// installClaude is Claude's install.sh (MOCK_INSTALL_FAIL, MOCK_INSTALL_FAIL_TIMES).
func (s *Server) installClaude(stderr io.Writer) error {
	flag := s.flag
	if flag("MOCK_INSTALL_FAIL") {
		return exitError{1}
	}
	// MOCK_INSTALL_FAIL_TIMES=N: the first N attempts fail (rate limit), then it works
	if n := s.Env["MOCK_INSTALL_FAIL_TIMES"]; n != "" {
		var limit, done int
		_, _ = fmt.Sscan(n, &limit)
		_, _ = fmt.Sscan(s.File("install-attempts"), &done)
		s.Write("install-attempts", fmt.Sprint(done+1)+"\n")
		if done < limit {
			return fail(stderr, 22, "curl: (22) The requested URL returned error: 429")
		}
	}
	s.Write("claude-ready", "")
	return nil
}
