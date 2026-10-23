package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// Passed through to the container when set; keep in sync with the RUN define in the Makefile
// (a test compares them).
var passEnv = []string{
	"HCLOUD_TOKEN", "CLOUDFLARE_API_TOKEN", "CLOUDFLARE_ZONE_ID", "VIPER_GH_TOKEN", "VIPER_GH_HOST",
	"VIPER_CLAUDE_TOKEN", "VIPER_ANTHROPIC_BASE_URL", "VIPER_ANTHROPIC_AUTH_TOKEN", "VIPER_CLAUDE_MODEL",
	"VIPER_CLAUDE_MAX_CONTEXT_TOKENS", "VIPER_HOMEBREW", "VIPER_BREW_TAPS", "VIPER_BREW_TRUST",
	"VIPER_BREW_PACKAGES", "VIPER_BREWFILE", "VIPER_SWAP", "VIPER_CODE_SERVER", "VIPER_CODE_SERVER_PASSWORD",
	"VIPER_GIT_USER_NAME", "VIPER_GIT_USER_EMAIL",
}

// host is what the container is set up from.
type host struct {
	uid, gid, home string
	dir            string // the checkout, mounted read-only as /repo
	passwd         string // generated passwd file (see runArgs)
	tty            bool
	tz             []string // docker arguments for the time zone
	sshConfigMount string   // target of a symlinked ~/.ssh/config outside ~/.ssh, "" if none
}

func (h host) sshDir() string   { return filepath.Join(h.home, ".ssh") }
func (h host) stateDir() string { return filepath.Join(h.home, ".local", "state", "hetzner-claude") }

// The container runs as the host user with the host's ~/.ssh and state directory mounted at the
// same paths, so keys, host CA and ssh config end up where the host's ssh and VS Code expect
// them. The image has no user for that uid: a passwd file with it and the host's home is
// mounted (ssh resolves ~ via the passwd entry, not $HOME).
func runArgs(h host, image string, extraEnv, args []string) []string {
	a := []string{"run", "--rm", "-i"}
	if h.tty {
		a = append(a, "-t")
	}
	a = append(a, "--user", h.uid+":"+h.gid, "-e", "HOME="+h.home)
	a = append(a, h.tz...)
	for _, n := range passEnv {
		a = append(a, "-e", n)
	}
	for _, e := range extraEnv {
		a = append(a, "-e", e)
	}
	a = append(a, "-v", h.passwd+":/etc/passwd:ro", "-v", h.sshDir()+":"+h.sshDir(), "-v", h.stateDir()+":"+h.stateDir())
	if h.sshConfigMount != "" {
		a = append(a, "-v", h.sshConfigMount+":"+h.sshConfigMount)
	}
	a = append(a, "-v", h.dir+":/repo:ro", image)
	return append(a, args...)
}

func passwdContent(uid, gid, home string) (string, error) {
	if strings.ContainsAny(home, ":\n") {
		return "", fmt.Errorf("home directory %q contains ':' or a newline", home)
	}
	return fmt.Sprintf("root:x:0:0:root:/root:/bin/bash\nrunner:x:%s:%s::%s:/bin/bash\n", uid, gid, home), nil
}

// TZ as given, else the zone name behind /etc/localtime (Linux, macOS), else that file itself
// (Linux, mounted into the container).
func timezoneArgs(tz string, readlink func(string) (string, error), exists func(string) bool, goos string) []string {
	if tz != "" {
		return []string{"-e", "TZ=" + tz}
	}
	if target, err := readlink("/etc/localtime"); err == nil {
		if _, name, ok := strings.Cut(target, "zoneinfo/"); ok && name != "" {
			return []string{"-e", "TZ=" + name}
		}
	}
	if goos == "linux" && exists("/etc/localtime") {
		return []string{"-v", "/etc/localtime:/etc/host-localtime:ro", "-e", "TZ=:/etc/host-localtime"}
	}
	return nil
}

// ~/.ssh/config may be a symlink into a dotfiles repo: its target is mounted too (at the same
// path), otherwise the scripts cannot add their Include line from inside the container.
func sshConfigTarget(home string) string {
	target, err := filepath.EvalSymlinks(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		return ""
	}
	ssh := filepath.Join(home, ".ssh")
	real, err := filepath.EvalSymlinks(ssh)
	if err != nil {
		real = ssh
	}
	for _, p := range []string{ssh, real} {
		if strings.HasPrefix(target, p+string(filepath.Separator)) {
			return ""
		}
	}
	return target
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(st, null) {
		return false
	}
	return true
}

func execute(c command, o options) error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("Windows is not supported; use viper inside WSL")
	}
	dir := o.dir
	if dir == "" {
		dir = "."
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	// the physical path, like make's $(CURDIR): a VM shares directories by their real location
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	if _, err := os.Stat(filepath.Join(dir, "setup.sh")); err != nil {
		return fmt.Errorf("%s is not a hetzner-viper checkout (run viper there or pass --dir)", dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	h := host{uid: fmt.Sprint(os.Getuid()), gid: fmt.Sprint(os.Getgid()), home: home, dir: dir, tty: isTerminal(os.Stdin)}
	for _, d := range []string{h.sshDir(), h.stateDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	content, err := passwdContent(h.uid, h.gid, home)
	if err != nil {
		return err
	}
	// before the passwd file exists: pulling or building can take minutes and be interrupted
	image, err := ensureImage(o)
	if err != nil {
		return err
	}
	// in the state directory: with a VM (Colima, Docker Desktop) only the home directory is
	// shared with Docker, the system's temp directory is not
	pw, err := os.CreateTemp(h.stateDir(), "passwd.")
	if err != nil {
		return err
	}
	defer os.Remove(pw.Name())
	if _, err := pw.WriteString(content); err != nil {
		return err
	}
	if err := pw.Chmod(0o644); err != nil {
		return err
	}
	pw.Close()
	h.passwd = pw.Name()
	h.tz = timezoneArgs(os.Getenv("TZ"), os.Readlink, func(p string) bool { _, err := os.Stat(p); return err == nil }, runtime.GOOS)
	h.sshConfigMount = sshConfigTarget(home)

	args := c.args(&o)
	return dockerRun(runArgs(h, image, o.extraEnv, args)...)
}

// replaced by the tests
var (
	// Ctrl-C reaches docker with the terminal (it passes it on to the container); viper itself
	// must survive it and wait, so that the passwd file is removed. SIGTERM and SIGHUP are
	// forwarded: they do not reach the child on their own.
	dockerRun = func(args ...string) error {
		cmd := exec.Command("docker", args...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			return err
		}
		sig := make(chan os.Signal, 4)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
		defer signal.Stop(sig)
		done := make(chan struct{})
		go func() {
			for {
				select {
				case s := <-sig:
					if s != syscall.SIGINT {
						_ = cmd.Process.Signal(s)
					}
				case <-done:
					return
				}
			}
		}()
		err := cmd.Wait()
		close(done)
		return err
	}
	dockerQuiet = func(args ...string) error { return exec.Command("docker", args...).Run() }
)
