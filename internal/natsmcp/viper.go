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
	// RestartWait is the first pause before the tunnel is started again; it doubles with each try (--restart-wait, for tests).
	RestartWait time.Duration
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
	o := ViaOptions{Name: "local", SendPerMin: DefaultViaSendPerMin, RestartWait: time.Second}
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
		case "--viper", "--name", "--dir", "--files", "--push-from", "--send-per-min", "--restart-wait":
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
		return o.setFolder(flag, v)
	case "--push-from":
		return o.setPushFrom(v)
	case "--restart-wait":
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return fmt.Errorf("--restart-wait needs a duration such as 1s: %q", v)
		}
		o.RestartWait = d
	case "--send-per-min":
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return fmt.Errorf("--send-per-min needs a number, 0 for no limit: %q", v)
		}
		o.SendPerMin = n
	}
	return nil
}

// setFolder stores --dir or --files: an absolute path.
func (o *ViaOptions) setFolder(flag, v string) error {
	if !filepath.IsAbs(v) {
		return fmt.Errorf("%s needs an absolute path: %q", flag, v)
	}
	if flag == "--dir" {
		o.Dir = v
	} else {
		o.Files = v
	}
	return nil
}

// setPushFrom stores --push-from: machine names with commas.
func (o *ViaOptions) setPushFrom(v string) error {
	for _, m := range strings.Split(v, ",") {
		if m = strings.TrimSpace(m); m != "" {
			if !ValidName(m) {
				return fmt.Errorf("--push-from: %q is not a machine name", m)
			}
			o.PushFrom = append(o.PushFrom, m)
		}
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
	// CAPEM is the certificate as it was when the line came: the file is deleted when the child ends, and a restarted child writes a new one.
	CAPEM []byte `json:"-"`
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
	// the end of the context is the end of stdin, like any other end: the child then closes the tunnel itself (a kill would leave ssh behind)
	cmd.Cancel = stdin.Close
	cmd.WaitDelay = 5 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, Ready{}, err
	}
	if err := cmd.Start(); err != nil {
		return nil, Ready{}, fmt.Errorf("starting %s: %w", viper, err)
	}
	c := &tunnelChild{cmd: cmd, stdin: stdin, out: bufio.NewReaderSize(stdout, maxReadyLine+1), done: make(chan struct{}), errs: errs}
	r, err := c.readReady(ctx, o, version)
	if err != nil {
		return nil, Ready{}, err
	}
	if r.CAFile != "" {
		if r.CAPEM, err = os.ReadFile(r.CAFile); err != nil {
			c.reap()
			return nil, Ready{}, fmt.Errorf("reading the certificate: %w", err)
		}
	}
	// Wait only once the line is read: it closes the pipe
	go func() { _ = cmd.Wait(); close(c.done) }()
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
		if r.err != nil { // the pipe ended: the child is gone, and says why
			c.reap()
			return Ready{}, c.childError(r.err)
		}
		pr, err := ParseReady(bytes.TrimSpace(r.line), o, version)
		if err != nil {
			c.reap()
		}
		return pr, err
	case <-time.After(readyWait):
		c.reap()
		return Ready{}, errors.New("viper nats tunnel did not come up in time")
	case <-ctx.Done():
		c.reap()
		return Ready{}, ctx.Err()
	}
}

// reap ends a child that did not come up: it is given a moment to end by itself (its exit status says what was wrong), then killed.
func (c *tunnelChild) reap() {
	_ = c.stdin.Close()
	w := make(chan struct{})
	go func() { _ = c.cmd.Wait(); close(w) }()
	select {
	case <-w:
	case <-time.After(2 * time.Second):
		_ = c.cmd.Process.Kill()
		<-w
	}
}

// childError is err with what the child said on stderr, or the request for --yes if that was its exit status; the child has been reaped.
func (c *tunnelChild) childError(err error) error {
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

// restartTries is how often a dead tunnel is started again before the server gives up; the pauses between are RestartWait, twice that, four
// times, and so on.
const restartTries = 5

// supervisor keeps the child alive: when it ends (the computer slept, the network went) it is started again on the same port, which the
// NATS client of the server connects to again by itself.
type supervisor struct {
	version string
	opts    ViaOptions
	viper   string
	stderr  io.Writer
	log     Logger

	mu    sync.Mutex
	child *tunnelChild
	ready Ready
	code  int // the exit status when the supervisor gave up (0: it did not)
	why   string
	// closing: the server ends, so a child that ends is not started again
	closing bool
}

func (v *supervisor) current() (*tunnelChild, Ready) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.child, v.ready
}

func (v *supervisor) stop() {
	v.mu.Lock()
	v.closing = true
	c := v.child
	v.mu.Unlock()
	if c != nil {
		c.stop()
	}
}

func (v *supervisor) isClosing() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.closing
}

// quickDeath is how long a tunnel must have been up for its end not to count against the tries: one that dies again at once is a
// network or a server that is not well, and is not started again for ever.
const quickDeath = time.Minute

func (v *supervisor) giveUp(code int, why string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.code, v.why = code, why
}

func (v *supervisor) gaveUp() (int, string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.code, v.why
}

// watch restarts the child each time it ends, until ctx is done or the server gives up (then cancel is called).
func (v *supervisor) watch(ctx context.Context, cancel context.CancelFunc) {
	quick := 0 // how many tunnels in a row died soon after they came up
	up := time.Now()
	for {
		child, old := v.current()
		select {
		case <-child.done:
		case <-ctx.Done():
			return
		}
		if v.isClosing() {
			return
		}
		v.log.Printf("viper nats tunnel ended: %s", child.errs.String())
		if time.Since(up) < quickDeath {
			quick++
		} else {
			quick = 0
		}
		if quick >= restartTries {
			v.giveUp(1, fmt.Sprintf("the tunnel to the NATS server ended %d times in a row soon after it came up", quick))
			cancel()
			return
		}
		next, ready, err := v.restart(ctx, old)
		if err != nil {
			if ctx.Err() == nil && !v.isClosing() {
				code := 1
				if errors.Is(err, errCertificateChanged) {
					code = ExitCertificateChanged
				}
				v.giveUp(code, err.Error())
			}
			cancel()
			return
		}
		v.mu.Lock()
		v.child, v.ready = next, ready
		v.mu.Unlock()
		up = time.Now()
		v.log.Printf("the tunnel is up again (viper nats tunnel pid %d)", ready.PID)
	}
}

var errCertificateChanged = errors.New("the server of the group has another certificate now; this connection keeps the old one: /mcp connects again")

// restart starts the child on the port of the one that ended, up to restartTries times with growing pauses.
func (v *supervisor) restart(ctx context.Context, old Ready) (*tunnelChild, Ready, error) {
	port := portOf(old.URL)
	opts := v.opts
	opts.Dir = old.SettingsDir // the folder the group was found in the first time, whatever has changed since
	wait := v.opts.RestartWait
	var last error
	for try := 1; try <= restartTries; try++ {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, Ready{}, ctx.Err()
		}
		wait *= 2
		child, ready, err := startTunnel(ctx, v.viper, opts, port, v.stderr, v.version)
		if err == nil {
			if certificateChanged(old, ready) {
				child.stop()
				return nil, Ready{}, errCertificateChanged
			}
			return child, ready, nil
		}
		last = err
		v.log.Printf("the tunnel did not come up again (try %d of %d): %v", try, restartTries, err)
	}
	return nil, Ready{}, fmt.Errorf("the tunnel to the NATS server could not be opened again after %d tries: %w", restartTries, last)
}

// portOf is the port of an address such as tls://127.0.0.1:41000 (0 if there is none).
func portOf(url string) int {
	if i := strings.LastIndex(url, ":"); i >= 0 {
		n, _ := strconv.Atoi(url[i+1:])
		return n
	}
	return 0
}

// certificateChanged tells whether the server has another certificate than the one the connection was made with. The bytes are those
// read when the line ready came (the files are gone by then: the child that wrote one deletes it when it ends).
func certificateChanged(old, now Ready) bool {
	return !bytes.Equal(old.CAPEM, now.CAPEM)
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
	cfg := viaConfig(o, ready, version)
	log := NewFileLogger(stderr, cfg.LogFile, 1<<20)
	defer log.Close()
	sup := &supervisor{version: version, opts: o, viper: viper, stderr: stderr, log: log, child: child, ready: ready}
	log.Printf("starting %s as %s in the group %s (through %s; viper nats tunnel pid %d)", version, cfg.Names.Me, cfg.Names.Group, ready.URL, ready.PID)
	runCtx, cancel := context.WithCancel(ctx)
	defer func() { sup.stop(); cancel() }() // the child first: it closes the tunnel when its stdin ends
	go sup.watch(runCtx, cancel)
	err = Run(runCtx, cfg, version, stdin, stdout, log)
	if code, why := sup.gaveUp(); code != 0 { // the supervisor ended it: that is the reason, not the cancelled context
		fmt.Fprintln(stderr, "viper-nats-mcp --via-viper:", why)
		return code
	}
	if err != nil {
		fmt.Fprintln(stderr, "viper-nats-mcp:", err)
		return 1
	}
	return 0
}
