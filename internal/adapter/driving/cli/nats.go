package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/natsmcp"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

// viper nats (docs/plan/nats-cli.md, stage 1): peers, listen, send, broadcast and request, as one of
// the machines of a NATS group. viper runs viper-nats-mcp on that machine over ssh; the machine speaks
// with its own key, so the others see it as the sender.

// natsArgs are the words and options of viper nats, checked.
type natsArgs struct {
	cmd driving.NATSCommand
	// attachPath and attachKind are the file that goes with the message (--pdf, --image or --zip); it is read once the group is known
	// (its size limit is a setting of the group).
	attachPath, attachKind string
	// group is the name of the group (the second word).
	group string
}

// parseNatsArgs reads the words after viper nats: SUBCOMMAND GROUP [TEXT|-].
func parseNatsArgs(o *options, stdin io.Reader) (natsArgs, error) {
	usage := usageError("nats")
	if len(o.args) < 2 {
		return natsArgs{}, errors.New(usage)
	}
	sub, group, rest := o.args[0], o.args[1], o.args[2:]
	c := driving.NATSCommand{Name: sub, To: o.to, Topic: o.topic, Timeout: o.timeout, Query: o.query, All: o.all}
	if err := checkNatsOptions(o, sub); err != nil {
		return natsArgs{}, err
	}
	attachPath, attachKind, err := natsAttachment(o)
	if err != nil {
		return natsArgs{}, err
	}
	switch sub {
	case "send", "broadcast", "request":
		if attachPath != "" && len(rest) == 0 && o.file == "" { // a file and no text
			break
		}
		if c.Text, err = natsTextOf(o, sub, rest, stdin, usage); err != nil {
			return natsArgs{}, err
		}
	case "fetch":
		if err := fetchWords(o, rest, usage, &c); err != nil {
			return natsArgs{}, err
		}
	case "init", "revoke", "peers", "topics", "listen":
		if len(rest) > 0 {
			return natsArgs{}, fmt.Errorf("viper nats %s takes no text: %q\n%s", sub, rest[0], usage)
		}
	default:
		return natsArgs{}, fmt.Errorf("viper nats: unknown subcommand %q\n%s", sub, usage)
	}
	return natsArgs{cmd: c, group: group, attachPath: attachPath, attachKind: attachKind}, nil
}

// fetchWords reads viper nats fetch GROUP ID: the checksum comes from --sha256, the folder from --out (default: here).
func fetchWords(o *options, rest []string, usage string, c *driving.NATSCommand) error {
	if len(rest) != 1 {
		return fmt.Errorf("viper nats fetch needs the ID of the file (from viper nats listen)\n%s", usage)
	}
	c.ID, c.SHA256, c.Out = rest[0], o.sha256, o.out
	if c.Out == "" {
		c.Out = "."
	}
	return nil
}

// natsAttachment reads --pdf, --image and --zip: at most one of them.
func natsAttachment(o *options) (path, kind string, err error) {
	n := 0
	for k, p := range map[string]string{"pdf": o.pdf, "image": o.image, "zip": o.zip} {
		if p != "" {
			n++
			path, kind = p, k
		}
	}
	if n > 1 {
		return "", "", errors.New("viper nats: one file at most: --pdf, --image or --zip")
	}
	return path, kind, nil
}

// checkNatsOptions refuses an option the subcommand does not use, so a typo does not go unnoticed.
func checkNatsOptions(o *options, sub string) error {
	use := map[string][]string{
		"send":      {"to", "file", "pdf", "image", "zip"},
		"request":   {"to", "timeout", "file", "pdf", "image", "zip"},
		"broadcast": {"topic", "file", "pdf", "image", "zip"},
		"fetch":     {"sha256", "out"},
		"peers":     {"timeout", "query"},
		"topics":    {"timeout", "query"},
		"listen":    {"topic", "all"},
		"init":      {},
		"revoke":    {},
	}[sub]
	given := map[string]bool{"to": o.to != "", "topic": o.topic != "", "timeout": o.timeout != 0, "query": o.query, "all": o.all, "file": o.file != "", "pdf": o.pdf != "", "image": o.image != "", "zip": o.zip != "", "sha256": o.sha256 != "", "out": o.out != ""}
	for opt, set := range given {
		if set && !slices.Contains(use, opt) {
			return fmt.Errorf("viper nats %s: --%s is not used by this subcommand\n%s\nviper nats %s --help describes its options", sub, opt, usageLine("nats"), sub)
		}
	}
	for _, opt := range use {
		if opt == "to" && o.to == "" || opt == "topic" && o.topic == "" && sub == "broadcast" || opt == "sha256" && o.sha256 == "" {
			return fmt.Errorf("viper nats %s: --%s is needed\n%s\nviper nats %s --help describes its options", sub, opt, usageLine("nats"), sub)
		}
	}
	return nil
}

// natsText is the text of a message: the word itself, or stdin for "-" (one final new line is not part of it).
func natsTextOf(o *options, sub string, rest []string, stdin io.Reader, usage string) (string, error) {
	if o.file != "" { // the text is in a file (- is stdin)
		if len(rest) != 0 {
			return "", fmt.Errorf("viper nats %s: the text is either a word or --file, not both\n%s", sub, usage)
		}
		if o.file == "-" {
			return natsText("-", stdin)
		}
		f, err := os.Open(o.file)
		if err != nil {
			return "", fmt.Errorf("viper nats %s: %w", sub, err)
		}
		defer func() { _ = f.Close() }() // read only
		return natsText("-", f)
	}
	if len(rest) != 1 {
		return "", fmt.Errorf("viper nats %s needs the text (or - for stdin, or --file FILE)\n%s", sub, usage)
	}
	return natsText(rest[0], stdin)
}

func natsText(word string, stdin io.Reader) (string, error) {
	if word != "-" {
		return word, nil
	}
	b, err := io.ReadAll(io.LimitReader(stdin, natsmcp.DefaultMaxText+1))
	if err != nil {
		return "", err
	}
	if len(b) > natsmcp.DefaultMaxText {
		return "", fmt.Errorf("the text has more than %d bytes", natsmcp.DefaultMaxText)
	}
	return strings.TrimSuffix(string(b), "\n"), nil
}

func nativeNats(o *options) error {
	if err := o.host().need("ssh"); err != nil {
		return err
	}
	env, err := o.env()
	if err != nil {
		return err
	}
	return runNats(context.Background(), o, env, o.host().Stdin, o.host().Stdout, o.host().Stderr)
}

// natsMachine is a machine of the group, as far as viper nats needs it.
type natsMachine struct {
	name string
	cfg  *model.Machine
}

func (m natsMachine) usable() bool {
	return m.cfg.NATS.Enabled && m.cfg.NATS.MCP && exists(m.cfg.SSHConfig)
}

// natsPrepare checks and reads what the command needs before anything is connected: the file of a message, and that fetch has the key
// of the operator (it puts a file on this computer). It says whether the command goes as the operator.
func natsPrepare(o *options, g *natsGroup, args *natsArgs) (asOperator bool, err error) {
	if args.attachPath != "" {
		if args.cmd.Attach, err = loadAttachment(g, *args); err != nil {
			return false, err
		}
	}
	asOperator = o.from == g.operator || (o.from == "" && exists(g.keyFile()))
	if args.cmd.Name == "fetch" && !asOperator {
		return false, fmt.Errorf("viper nats fetch needs the key of the operator of the group (viper nats init %s): it puts the file on this computer", args.group)
	}
	return asOperator, nil
}

func runNats(ctx context.Context, o *options, env settings.Env, in io.Reader, stdout, stderr io.Writer) error {
	args, err := parseNatsArgs(o, in)
	if err != nil {
		return err
	}
	switch args.cmd.Name {
	case "init":
		return runNatsInit(ctx, o, env, args.group, stdout)
	case "revoke":
		return runNatsRevoke(ctx, o, env, args.group, stdout)
	}
	g, err := loadNatsGroup(o, env, args.group)
	if err != nil {
		return err
	}
	if args.cmd.To != "" && g.ms[args.cmd.To].cfg == nil {
		return fmt.Errorf("%s is not a machine of the group %s", args.cmd.To, args.group)
	}
	if args.cmd.Name == "topics" {
		reportConfiguredTopics(o, g, stdout)
	}
	asOperator, err := natsPrepare(o, g, &args)
	if err != nil {
		return err
	}
	ctx, stop := notifyContext(ctx)
	defer stop()
	if asOperator {
		return runNatsAsOperator(ctx, o, g, args.cmd, stdout, stderr)
	}
	sender, err := natsSender(o, g.order, g.ms, args.cmd.To)
	if err != nil {
		return err
	}
	if err := natsPushCheck(o, args.cmd, sender, g.order, g.ms, stderr); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "Sender: %s (a machine of the group %s; the others see it as the sender)\n", sender, args.group)
	return natsRun(ctx, o, args.cmd, sender, sshRemote(o, g.ms[sender]), stdout, stderr)
}

// runNatsAsOperator speaks under the name of the operator, from this computer.
func runNatsAsOperator(ctx context.Context, o *options, g *natsGroup, c driving.NATSCommand, stdout, stderr io.Writer) error {
	if _, err := os.Stat(g.keyFile()); err != nil {
		return fmt.Errorf("the operator %s has no key here (%s): run viper nats init %s", g.operator, g.keyFile(), g.name)
	}
	sess, err := openOperator(ctx, o, g)
	if err != nil {
		return err
	}
	defer sess.close()
	fmt.Fprintf(stderr, "Sender: %s (the operator of the group %s, from this computer)\n", g.operator, g.name)
	return natsRun(ctx, o, c, g.operator, sess.remote, stdout, stderr)
}

// natsSender is the machine that speaks: --from, else the first usable one that is not the receiver.
func natsSender(o *options, order []string, ms map[string]natsMachine, to string) (string, error) {
	if o.from != "" {
		m, ok := ms[o.from]
		switch {
		case !ok:
			return "", fmt.Errorf("%s is not a machine of the group %s", o.from, o.group)
		case !m.cfg.NATS.Enabled || !m.cfg.NATS.MCP:
			return "", fmt.Errorf("%s has no NATS_MCP=true: viper nats runs viper-nats-mcp on it (viper up %s)", o.from, o.from)
		case !exists(m.cfg.SSHConfig):
			return "", fmt.Errorf("No SSH config for %s: run viper up %s first", m.cfg.ServerName, o.from)
		}
		return o.from, nil
	}
	m, err := o.host().Services.NATS.ChooseAccess(order, "", to, func(name string) bool { return ms[name].usable() })
	if err != nil {
		return "", errors.New("no machine of the group can speak: viper nats needs one with NATS_ENABLED, NATS_MCP=true and a running server (viper up); --from names one")
	}
	return m, nil
}

// natsPushCheck warns when the message would be pushed into the session of an agent as if the sender had
// written it (the machine is in NATS_MCP_PUSH_FROM of a receiver): that skips the human who decided who is trusted.
func natsPushCheck(o *options, c driving.NATSCommand, sender string, order []string, ms map[string]natsMachine, stderr io.Writer) error {
	if c.Name != "send" && c.Name != "broadcast" && c.Name != "request" {
		return nil
	}
	var hit []string
	for _, name := range order {
		if name == sender || (c.To != "" && name != c.To) {
			continue
		}
		if slices.Contains(ms[name].cfg.NATS.MCPPushFrom, sender) {
			hit = append(hit, name)
		}
	}
	if len(hit) == 0 {
		return nil
	}
	if !o.yes {
		return fmt.Errorf("%s is in NATS_MCP_PUSH_FROM of %s: the agent there gets your text pushed into its session as a message from %s. Use --yes to send anyway, or --from another machine", sender, strings.Join(hit, ", "), sender)
	}
	fmt.Fprintf(stderr, "Note: %s is in NATS_MCP_PUSH_FROM of %s; your text reaches the agent there as a message from %s.\n", sender, strings.Join(hit, ", "), sender)
	return nil
}

// sshRemote is driving.NATSRemote over ssh: the text goes to stdin, the program is quoted word by word, and
// nothing is tried twice (a send that was repeated would arrive twice).
func sshRemote(o *options, m natsMachine) driving.NATSRemote {
	return func(ctx context.Context, _, stdin string, stdout, stderr io.Writer, words ...string) error {
		st := o.host().StreamTool
		if st == nil {
			return errors.New("viper nats cannot run programs here")
		}
		args := []string{"-F", m.cfg.SSHConfig, "-o", "BatchMode=yes", m.cfg.SSHAlias, model.QuoteAll(words...)}
		if stdin == "" {
			args = append([]string{"-n"}, args...)
		}
		return st(ctx, stdin, stdout, stderr, "ssh", args...)
	}
}

func natsRun(ctx context.Context, o *options, c driving.NATSCommand, sender string, remote driving.NATSRemote, stdout, stderr io.Writer) error {
	show := func(e model.NATSLine) {
		if o.asJSON {
			writeJSONLine(stdout, e)
		} else if e.Via != "presence" && e.Via != "topics" {
			fmt.Fprintln(stdout, natsLine(e, sender))
		}
	}
	res, err := o.host().Services.NATS.Run(ctx, remote, sender, c, stderr, show)
	if err != nil {
		return exitOrError(err)
	}
	return natsReport(o, c, res, stdout)
}

// exitOrError turns the end of the program on the machine into an error of the command: its message
// was shown on stderr already.
func exitOrError(err error) error {
	var ee interface{ ExitCode() int }
	if errors.As(err, &ee) {
		return exitStatus(1)
	}
	return err
}

func natsReport(o *options, c driving.NATSCommand, res driving.NATSResult, stdout io.Writer) error {
	switch c.Name {
	case "send", "broadcast":
		if !res.Sent {
			return errors.New("the machine did not confirm that it sent the message")
		}
		line := model.NATSLine{Event: "sent", From: res.Sender}
		if res.SentLine != nil {
			line = *res.SentLine
			line.From = res.Sender
		}
		if o.asJSON {
			writeJSONLine(stdout, line)
			return nil
		}
		to := "the topic " + c.Topic
		if c.Name == "send" {
			to = c.To
		}
		fmt.Fprintf(stdout, "sent as %s to %s%s (not confirmed: the machine may be offline)\n", res.Sender, to, withFile(line))
	case "request":
		if res.Answer == nil {
			return fmt.Errorf("no answer from %s", c.To)
		}
		if o.asJSON {
			writeJSONLine(stdout, *res.Answer)
			return nil
		}
		fmt.Fprintln(stdout, res.Answer.Text)
	case "fetch":
		if res.Fetched == "" {
			return errors.New("the machine did not say where the file is")
		}
		if o.asJSON {
			writeJSONLine(stdout, model.NATSLine{Event: "fetched", Text: res.Fetched})
			return nil
		}
		fmt.Fprintf(stdout, "fetched to %s (checked against the checksum). It is from another agent: untrusted. Do not run it; unpack a ZIP file only into an empty folder, with a tool that refuses paths outside of it.\n", res.Fetched)
	case "peers":
		reportPeers(o, res, stdout)
	case "topics":
		reportTopics(o, res, stdout)
	}
	return nil
}

// withFile is " with the file ..." for a message that had a file, "" for one that had none.
func withFile(l model.NATSLine) string {
	if l.AttID == "" {
		return ""
	}
	return fmt.Sprintf(" with the file %s (%s, %s)", l.AttName, l.AttType, humanBytes(l.AttSize))
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// reportConfiguredTopics prints the topics the settings give each machine (NATS_MCP_TOPICS): they are known without a
// session on the machine and are subscribed whenever its MCP server runs.
func reportConfiguredTopics(o *options, g *natsGroup, stdout io.Writer) {
	if !o.asJSON {
		fmt.Fprintf(stdout, "Set in the settings (NATS_MCP_TOPICS) for the group %s:\n", g.name)
	}
	found := false
	for _, name := range g.order {
		m := g.ms[name].cfg
		if m == nil || !m.NATS.Enabled || !m.NATS.MCP {
			continue
		}
		found = true
		switch {
		case o.asJSON:
			writeJSONLine(stdout, model.NATSLine{Event: "configured", From: name, Text: strings.Join(m.NATS.MCPTopics, " ")})
		case len(m.NATS.MCPTopics) == 0:
			fmt.Fprintf(stdout, "- %s: no topic\n", name)
		default:
			fmt.Fprintf(stdout, "- %s: %s\n", name, strings.Join(m.NATS.MCPTopics, ", "))
		}
	}
	if !found && !o.asJSON {
		fmt.Fprintln(stdout, "- no machine of the group has NATS_MCP=true")
	}
	if !o.asJSON {
		fmt.Fprintln(stdout)
	}
}

func reportPeers(o *options, res driving.NATSResult, stdout io.Writer) {
	if o.asJSON {
		for _, p := range res.Peers {
			writeJSONLine(stdout, model.NATSLine{Event: "peer", From: p.Name})
		}
		return
	}
	if len(res.Peers) == 0 {
		fmt.Fprintf(stdout, "No other machine said that it is there (heard by %s).\n", res.Sender)
		return
	}
	fmt.Fprintf(stdout, "Present (heard by %s):\n", res.Sender)
	for _, p := range res.Peers {
		fmt.Fprintf(stdout, "- %s\n", p.Name)
	}
}

// reportTopics prints each machine with the topics it receives, and then each topic with its machines.
func reportTopics(o *options, res driving.NATSResult, stdout io.Writer) {
	if o.asJSON {
		for _, p := range res.Peers {
			writeJSONLine(stdout, model.NATSLine{Event: "peer", From: p.Name, Text: strings.Join(p.Topics, " ")})
		}
		return
	}
	if len(res.Peers) == 0 {
		fmt.Fprintf(stdout, "No other machine said that it is there (heard by %s).\n", res.Sender)
		return
	}
	fmt.Fprintf(stdout, "Subscribed now, as the running machines say (heard by %s):\n", res.Sender)
	by := map[string][]string{}
	for _, p := range res.Peers {
		switch {
		case !p.TopicsKnown:
			fmt.Fprintf(stdout, "- %s: has not said (an older viper-nats-mcp, or its announcement has not come yet)\n", p.Name)
		case len(p.Topics) == 0:
			fmt.Fprintf(stdout, "- %s: no topic\n", p.Name)
		default:
			fmt.Fprintf(stdout, "- %s: %s\n", p.Name, strings.Join(p.Topics, ", "))
		}
		for _, t := range p.Topics {
			by[t] = append(by[t], p.Name)
		}
	}
	names := make([]string, 0, len(by))
	for t := range by {
		names = append(names, t)
	}
	sort.Strings(names)
	for _, t := range names {
		fmt.Fprintf(stdout, "  %s <- %s\n", t, strings.Join(by[t], ", "))
	}
}

// natsLine is a message as text; "from -> to" only for a message that is not for the sender of the command (listen --all).
func natsLine(e model.NATSLine, sender string) string {
	where := e.From
	if e.To != "" && e.To != sender && e.Via == "out" {
		where += " -> " + e.To
	}
	if e.Topic != "" {
		where += " [" + e.Topic + "]"
	}
	if e.Type == "req" {
		where += " (request)"
	}
	line := where + ": " + strings.ReplaceAll(e.Text, "\n", "\n    ")
	if e.AttID != "" { // how to fetch it: the ID and the checksum, which fetch needs (it checks the file against them)
		line += fmt.Sprintf("\n    [attachment: %s, %s, %s; fetch it with viper nats fetch GROUP %s --sha256 %s]", e.AttName, e.AttType, humanBytes(e.AttSize), e.AttID, e.AttSHA)
	}
	return line
}

func writeJSONLine(w io.Writer, e model.NATSLine) {
	b, _ := json.Marshal(e) // strings and booleans only
	fmt.Fprintf(w, "%s\n", b)
}

// loadAttachment reads the file for the message and checks it as the machine will: the size limit of the group, the type from the
// first bytes (it has to be the kind the option names), and for a ZIP file its contents. Nothing is connected before this passed.
func loadAttachment(g *natsGroup, a natsArgs) (*driving.NATSAttachment, error) {
	limit := g.objectsFileMax()
	if limit == 0 {
		return nil, fmt.Errorf("the group %s has no object store: set NATS_OBJECTS=true in its group file and make the server again (viper down --group %s, viper up --group %s)", g.name, g.name, g.name)
	}
	if st, err := os.Stat(a.attachPath); err != nil { // look before opening: opening a FIFO blocks
		return nil, err
	} else if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is no regular file", a.attachPath)
	}
	f, err := os.Open(a.attachPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read only
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is no regular file", a.attachPath)
	}
	if st.Size() < 1 || st.Size() > limit {
		return nil, fmt.Errorf("%s has %d bytes, at least 1 and at most %d are allowed (NATS_OBJECTS_FILE_MAX)", a.attachPath, st.Size(), limit)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit { // it grew after it was measured
		return nil, fmt.Errorf("%s is larger than the %d bytes that are allowed (NATS_OBJECTS_FILE_MAX)", a.attachPath, limit)
	}
	typ := natsmcp.Classify(data)
	if !kindFits(a.attachKind, typ) {
		return nil, fmt.Errorf("%s is %s, not a %s: --%s takes %s", a.attachPath, describeType(typ), a.attachKind, a.attachKind, kindWords(a.attachKind))
	}
	if err := natsmcp.CheckAttachment(typ, data); err != nil {
		return nil, fmt.Errorf("%s: %w", a.attachPath, err)
	}
	return &driving.NATSAttachment{Name: natsmcp.CleanName(filepath.Base(a.attachPath)), Data: data}, nil
}

func kindFits(kind, typ string) bool {
	switch kind {
	case "pdf":
		return typ == natsmcp.TypePDF
	case "zip":
		return typ == natsmcp.TypeZIP
	case "image":
		return typ == natsmcp.TypePNG || typ == natsmcp.TypeJPEG || typ == natsmcp.TypeGIF || typ == natsmcp.TypeWebP
	}
	return false
}

func kindWords(kind string) string {
	switch kind {
	case "pdf":
		return "a PDF file"
	case "zip":
		return "a ZIP file"
	}
	return "an image (PNG, JPEG, GIF or WebP)"
}

func describeType(typ string) string {
	if typ == "" {
		return "none of the types viper sends (judged by its first bytes)"
	}
	return "a " + typ + " file"
}
