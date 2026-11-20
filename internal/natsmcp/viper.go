package natsmcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
)

// The mode --via-viper (docs/plan/nats-lokal.md §4): the MCP server of a Claude Code session on the computer of a user. It has no
// connection.env: it asks viper nats tunnel for the tunnel to the NATS server of a group and uses the line ready that viper writes.

// ViaOptions are the options of viper-nats-mcp --via-viper GROUP.
type ViaOptions struct {
	Group      string
	Viper      string // --viper: the program viper (default: next to this program, then in PATH)
	Name       string // --name: the identity (default local)
	Dir        string // --dir: the folder with the settings of the group (absolute)
	Files      string // --files: the folder attachments may come from (absolute); without it no attachments
	PushFrom   []string
	Yes        bool
	SendPerMin int
}

// DefaultViaSendPerMin is the limit of what the agent of this mode sends and asks per minute.
const DefaultViaSendPerMin = 20

// ExitNeedsYes is the exit status of viper nats tunnel that says that --yes is missing (cli.exitNeedsYes).
const ExitNeedsYes = 3

// ExitCertificateChanged is the exit status of this program when the server of the group has another certificate after a restart of the
// tunnel: a connected client keeps the old one, so the session must reconnect (/mcp).
const ExitCertificateChanged = 4

const maxReadyLine = 4096

// ParseViaArgs reads the words after --via-viper.
func ParseViaArgs(args []string) (ViaOptions, error) {
	o := ViaOptions{Name: "local", SendPerMin: DefaultViaSendPerMin}
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", a)
			}
			i++
			return args[i], nil
		}
		var err error
		var v string
		switch a {
		case "--yes":
			o.Yes = true
		case "--viper", "--name", "--dir", "--files", "--push-from", "--send-per-min":
			if v, err = value(); err == nil {
				err = o.set(a, v)
			}
		default:
			if strings.HasPrefix(a, "-") {
				return o, fmt.Errorf("unknown option %s", a)
			}
			rest = append(rest, a)
		}
		if err != nil {
			return o, err
		}
	}
	if len(rest) != 1 {
		return o, errors.New("usage: viper-nats-mcp --via-viper GROUP [--viper PATH] [--name NAME] [--dir ABSOLUTE-FOLDER] [--push-from m1,m2] [--files ABSOLUTE-FOLDER] [--send-per-min N] [--yes]")
	}
	o.Group = rest[0]
	if err := model.CheckNATSName("group", o.Group); err != nil {
		return o, err
	}
	return o, model.CheckNATSName("name", o.Name)
}

// set stores the value of an option that takes one.
func (o *ViaOptions) set(flag, v string) error {
	switch flag {
	case "--viper":
		o.Viper = v
	case "--name":
		o.Name = v
	case "--dir", "--files":
		if !filepath.IsAbs(v) {
			return fmt.Errorf("%s needs an absolute path: %q", flag, v)
		}
		if flag == "--dir" {
			o.Dir = v
		} else {
			o.Files = v
		}
	case "--push-from":
		for _, m := range strings.Split(v, ",") {
			if m = strings.TrimSpace(m); m != "" {
				if !ValidName(m) {
					return fmt.Errorf("--push-from: %q is not a machine name", m)
				}
				o.PushFrom = append(o.PushFrom, m)
			}
		}
	case "--send-per-min":
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return fmt.Errorf("--send-per-min needs a number, 0 for no limit: %q", v)
		}
		o.SendPerMin = n
	}
	return nil
}

// Ready is the line viper nats tunnel writes once the tunnel is open.
type Ready struct {
	Event          string `json:"event"`
	Version        string `json:"version"`
	Group          string `json:"group"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	ServerName     string `json:"server_name"`
	CAFile         string `json:"ca_file"`
	SeedFile       string `json:"seed_file"`
	SettingsDir    string `json:"settings_dir"`
	StateDir       string `json:"state_dir"`
	ObjectsFileMax int64  `json:"objects_file_max"`
	PID            int    `json:"pid"`
}

// ParseReady reads and checks the line (it is input from another program): at most one line of 4 KiB, the event ready, the address on this
// computer, files that exist with absolute paths. Fields it does not know are ignored.
func ParseReady(line []byte, o ViaOptions, version string) (Ready, error) {
	var r Ready
	if len(line) > maxReadyLine {
		return r, errors.New("the line ready is longer than 4 KiB")
	}
	if err := json.Unmarshal(line, &r); err != nil {
		return r, fmt.Errorf("the line ready is not JSON: %w", err)
	}
	if r.Event != "ready" {
		return r, fmt.Errorf("the first line is not ready: %q", r.Event)
	}
	if r.Group != o.Group || r.Name != o.Name {
		return r, fmt.Errorf("ready is for %s/%s, not %s/%s", r.Group, r.Name, o.Group, o.Name)
	}
	if r.Version != version {
		return r, fmt.Errorf("viper is %q, this program is %q: both must come from one build (--viper names another program)", r.Version, version)
	}
	if err := checkLocalURL(r); err != nil {
		return r, err
	}
	if err := checkReadyPaths(r); err != nil {
		return r, err
	}
	return r, nil
}

// checkReadyPaths checks the files and folders that ready names: absolute paths of what is there.
func checkReadyPaths(r Ready) error {
	for _, f := range [][2]string{{"ca_file", r.CAFile}, {"seed_file", r.SeedFile}} {
		if f[1] == "" && f[0] == "ca_file" && strings.HasPrefix(r.URL, "nats://") {
			continue
		}
		if st, err := os.Stat(f[1]); err != nil || !filepath.IsAbs(f[1]) || !st.Mode().IsRegular() {
			return fmt.Errorf("%s in ready is no absolute path of a file: %q", f[0], f[1])
		}
	}
	if st, err := os.Stat(r.SettingsDir); err != nil || !filepath.IsAbs(r.SettingsDir) || !st.IsDir() {
		return fmt.Errorf("settings_dir in ready is no absolute path of a folder: %q", r.SettingsDir)
	}
	if !filepath.IsAbs(r.StateDir) || r.ObjectsFileMax < 0 {
		return errors.New("state_dir or objects_file_max in ready is wrong")
	}
	return nil
}

// checkLocalURL accepts only the address of the tunnel on this computer (tls:// with a certificate, nats:// without).
func checkLocalURL(r Ready) error {
	scheme := "tls://"
	if r.CAFile == "" {
		scheme = "nats://"
	}
	rest, ok := strings.CutPrefix(r.URL, scheme)
	if !ok {
		return fmt.Errorf("url in ready is not %s127.0.0.1:PORT: %q", scheme, r.URL)
	}
	host, port, err := net.SplitHostPort(rest)
	if n, perr := strconv.Atoi(port); err != nil || host != "127.0.0.1" || perr != nil || n < 1 || n > 65535 {
		return fmt.Errorf("url in ready is not %s127.0.0.1:PORT: %q", scheme, r.URL)
	}
	return nil
}

// FindViper says which program viper to start: the one --viper names, else the one next to this program, else one in PATH. Never one
// in the current folder (Claude Code starts this program in the folder of a project).
func FindViper(flag string) (string, error) {
	name := "viper"
	if runtime.GOOS == "windows" {
		name = "viper.exe"
	}
	if flag != "" {
		return regularFile(flag)
	}
	if self, err := os.Executable(); err == nil {
		if p, err := regularFile(filepath.Join(filepath.Dir(self), name)); err == nil {
			return p, nil
		}
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", errors.New("viper was not found next to this program or in PATH: give --viper")
	}
	return regularFile(p)
}

func regularFile(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(abs); err != nil || !st.Mode().IsRegular() {
		return "", fmt.Errorf("%s is no program", abs)
	}
	return abs, nil
}

// childEnv is the environment of viper nats tunnel: what ssh and the settings need, nothing else.
func childEnv(environ []string) []string {
	keep := map[string]bool{"HOME": true, "USERPROFILE": true, "PATH": true, "SSH_AUTH_SOCK": true, "SYSTEMROOT": true, "TEMP": true, "TMP": true, "TMPDIR": true,
		"COMSPEC": true, "PATHEXT": true, "APPDATA": true, "LOCALAPPDATA": true, "USER": true, "LOGNAME": true}
	var out []string
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if keep[strings.ToUpper(k)] || strings.HasPrefix(k, "XDG_") || strings.HasPrefix(k, "VIPER_") {
			out = append(out, kv)
		}
	}
	return out
}

// tunnelChild is a running viper nats tunnel.
type tunnelChild struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	out   *bufio.Reader
	done  chan struct{} // closed when the process has ended
	errs  *tail
}

// tail keeps the last bytes of what the child said on stderr, for the message when it fails.
type tail struct {
	mu sync.Mutex
	b  []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > 2048 {
		t.b = t.b[len(t.b)-2048:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.b))
}

// startTunnel starts viper nats tunnel for the options (port 0: any) and waits for its line ready.
func startTunnel(ctx context.Context, viper string, o ViaOptions, port int, stderr io.Writer, version string) (*tunnelChild, Ready, error) {
	args := []string{"nats", "tunnel", o.Group, "--name", o.Name}
	if o.Dir != "" {
		args = append(args, "--dir", o.Dir)
	}
	if port != 0 {
		args = append(args, "--port", strconv.Itoa(port))
	}
	if o.Yes {
		args = append(args, "--yes")
	}
	cmd := exec.CommandContext(ctx, viper, args...) //nolint:gosec // the program is the one the user named or the one next to this one; no shell
	cmd.Env = childEnv(os.Environ())
	errs := &tail{}
	cmd.Stderr = io.MultiWriter(stderr, errs)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, Ready{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, Ready{}, err
	}
	if err := cmd.Start(); err != nil {
		return nil, Ready{}, fmt.Errorf("starting %s: %w", viper, err)
	}
	c := &tunnelChild{cmd: cmd, stdin: stdin, out: bufio.NewReaderSize(stdout, maxReadyLine+1), done: make(chan struct{}), errs: errs}
	go func() { _ = cmd.Wait(); close(c.done) }()
	r, err := c.readReady(ctx, o, version)
	if err != nil {
		c.stop()
		return nil, Ready{}, err
	}
	return c, r, nil
}

// readyWait is how long the child may take for the tunnel (it looks for the server and opens ssh).
const readyWait = 90 * time.Second

func (c *tunnelChild) readReady(ctx context.Context, o ViaOptions, version string) (Ready, error) {
	type res struct {
		line []byte
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		line, err := c.out.ReadSlice('\n')
		ch <- res{append([]byte(nil), line...), err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return Ready{}, c.failure(r.err)
		}
		return ParseReady(bytes.TrimSpace(r.line), o, version)
	case <-c.done:
		return Ready{}, c.failure(errors.New("viper nats tunnel ended"))
	case <-time.After(readyWait):
		return Ready{}, errors.New("viper nats tunnel did not come up in time")
	case <-ctx.Done():
		return Ready{}, ctx.Err()
	}
}

// failure is an error with what the child said, and its exit status.
func (c *tunnelChild) failure(err error) error {
	<-waitOrTimeout(c.done, 2*time.Second)
	msg := c.errs.String()
	if c.cmd.ProcessState != nil && c.cmd.ProcessState.ExitCode() == ExitNeedsYes {
		return &NeedsYesError{Said: msg}
	}
	if msg != "" {
		return fmt.Errorf("%w: %s", err, msg)
	}
	return err
}

// NeedsYesError says that viper nats tunnel wants --yes (a machine of the group would get the text of this session pushed in).
type NeedsYesError struct{ Said string }

func (e *NeedsYesError) Error() string {
	return e.Said + " (start viper-nats-mcp with --yes to accept that)"
}

func waitOrTimeout(done <-chan struct{}, d time.Duration) <-chan struct{} {
	out := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-time.After(d):
		}
		close(out)
	}()
	return out
}

// stop ends the child: closing its stdin is the sign, then the process is killed if it does not go.
func (c *tunnelChild) stop() {
	_ = c.stdin.Close()
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		_ = c.cmd.Process.Kill()
		<-c.done
	}
}

// ViaViperMain is viper-nats-mcp --via-viper: it starts viper nats tunnel and serves MCP on stdin and stdout until stdin ends.
func ViaViperMain(version string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	o, err := ParseViaArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, "viper-nats-mcp --via-viper:", err)
		return 2
	}
	viper, err := FindViper(o.Viper)
	if err != nil {
		fmt.Fprintln(stderr, "viper-nats-mcp --via-viper:", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return viaRun(ctx, version, o, viper, stdin, stdout, stderr)
}

// viaConfig is the configuration of the server for the tunnel that ready describes.
func viaConfig(o ViaOptions, r Ready, version string) Config {
	cfg := DefaultConfig(Names{Group: o.Group, Me: o.Name}, r.SeedFile)
	cfg.URL, cfg.CAFile, cfg.TLSServerName = r.URL, r.CAFile, r.ServerName
	cfg.Local, cfg.Version, cfg.SendPerMin = true, version, o.SendPerMin
	for _, m := range o.PushFrom {
		cfg.PushFrom[m] = true
	}
	if o.Files != "" { // attachments only from the folder the user named
		cfg.AttachRoot, cfg.ObjectsFileMax = filepath.Clean(o.Files), r.ObjectsFileMax
	}
	dir := filepath.Join(r.StateDir, "nats-mcp")
	cfg.AttachDir = filepath.Join(dir, o.Group+"-"+o.Name, "attachments")
	cfg.LogFile = filepath.Join(dir, o.Group+"-"+o.Name+".log")
	cfg.Quiet = os.Getenv("VIPER_NATS_MCP_QUIET") == "1"
	return cfg
}

func viaRun(ctx context.Context, version string, o ViaOptions, viper string, stdin io.Reader, stdout, stderr io.Writer) int {
	child, ready, err := startTunnel(ctx, viper, o, 0, stderr, version)
	if err != nil {
		fmt.Fprintln(stderr, "viper-nats-mcp --via-viper:", err)
		var yes *NeedsYesError
		if errors.As(err, &yes) {
			return ExitNeedsYes
		}
		return 1
	}
	defer child.stop()
	cfg := viaConfig(o, ready, version)
	log := NewFileLogger(stderr, cfg.LogFile, 1<<20)
	defer log.Close()
	log.Printf("starting %s as %s in the group %s (through %s; viper nats tunnel pid %d)", version, cfg.Names.Me, cfg.Names.Group, ready.URL, ready.PID)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { // the tunnel is gone: the tools could not do anything any more
		select {
		case <-child.done:
			log.Printf("viper nats tunnel ended: %s", child.errs.String())
			cancel()
		case <-runCtx.Done():
		}
	}()
	if err := Run(runCtx, cfg, version, stdin, stdout, log); err != nil {
		select {
		case <-child.done:
			fmt.Fprintln(stderr, "viper-nats-mcp --via-viper: the tunnel to the NATS server ended; /mcp connects again")
		default:
			fmt.Fprintln(stderr, "viper-nats-mcp:", err)
		}
		return 1
	}
	return 0
}
