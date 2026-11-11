package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
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
	// group is the name of the group (the second word).
	group string
}

// parseNatsArgs reads the words after viper nats: SUBCOMMAND GROUP [TEXT|-].
func parseNatsArgs(o *options, stdin io.Reader) (natsArgs, error) {
	usage := usageLine("nats")
	if len(o.args) < 2 {
		return natsArgs{}, errors.New(usage)
	}
	sub, group, rest := o.args[0], o.args[1], o.args[2:]
	c := driving.NATSCommand{Name: sub, To: o.to, Topic: o.topic, Timeout: o.timeout, Query: o.query}
	if err := checkNatsOptions(o, sub); err != nil {
		return natsArgs{}, err
	}
	switch sub {
	case "send", "broadcast", "request":
		if len(rest) != 1 {
			return natsArgs{}, fmt.Errorf("viper nats %s needs the text (or - for stdin)\n%s", sub, usage)
		}
		text, err := natsText(rest[0], stdin)
		if err != nil {
			return natsArgs{}, err
		}
		c.Text = text
	case "peers", "listen":
		if len(rest) > 0 {
			return natsArgs{}, fmt.Errorf("viper nats %s takes no text: %q\n%s", sub, rest[0], usage)
		}
	default:
		return natsArgs{}, fmt.Errorf("viper nats: unknown subcommand %q\n%s", sub, usage)
	}
	return natsArgs{cmd: c, group: group}, nil
}

// checkNatsOptions refuses an option the subcommand does not use, so a typo does not go unnoticed.
func checkNatsOptions(o *options, sub string) error {
	use := map[string][]string{
		"send":      {"to"},
		"request":   {"to", "timeout"},
		"broadcast": {"topic"},
		"peers":     {"timeout", "query"},
		"listen":    {"topic"},
	}[sub]
	given := map[string]bool{"to": o.to != "", "topic": o.topic != "", "timeout": o.timeout != 0, "query": o.query}
	for opt, set := range given {
		if set && !slices.Contains(use, opt) {
			return fmt.Errorf("viper nats %s: --%s is not used by this subcommand\n%s", sub, opt, usageLine("nats"))
		}
	}
	for _, opt := range use {
		if opt == "to" && o.to == "" || opt == "topic" && o.topic == "" && sub == "broadcast" {
			return fmt.Errorf("viper nats %s: --%s is needed\n%s", sub, opt, usageLine("nats"))
		}
	}
	return nil
}

// natsText is the text of a message: the word itself, or stdin for "-" (one final new line is not part of it).
func natsText(word string, stdin io.Reader) (string, error) {
	if word != "-" {
		return word, nil
	}
	b, err := io.ReadAll(io.LimitReader(stdin, natsmcp.DefaultMaxText+1))
	if err != nil {
		return "", err
	}
	if len(b) > natsmcp.DefaultMaxText {
		return "", fmt.Errorf("the text on stdin has more than %d bytes", natsmcp.DefaultMaxText)
	}
	return strings.TrimSuffix(string(b), "\n"), nil
}

func nativeNats(o *options) error {
	if err := o.host().need("ssh"); err != nil {
		return err
	}
	env, err := o.host().Env()
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

func runNats(ctx context.Context, o *options, env settings.Env, in io.Reader, stdout, stderr io.Writer) error {
	args, err := parseNatsArgs(o, in)
	if err != nil {
		return err
	}
	o.group = args.group
	g, err := groupMachines(o, env)
	if err != nil {
		return err
	}
	ms := map[string]natsMachine{}
	for _, name := range g.Machines {
		c, err := o.host().Services.Settings.Load(settingsDir(o), name, env, false)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		ms[name] = natsMachine{name, c}
	}
	sender, err := natsSender(o, g.Machines, ms, args.cmd.To)
	if err != nil {
		return err
	}
	if args.cmd.To != "" && ms[args.cmd.To].cfg == nil {
		return fmt.Errorf("%s is not a machine of the group %s", args.cmd.To, args.group)
	}
	if err := natsPushCheck(o, args.cmd, sender, g.Machines, ms, stderr); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "Sender: %s (a machine of the group %s; the others see it as the sender)\n", sender, args.group)
	ctx, stop := notifyContext(ctx)
	defer stop()
	return natsRun(ctx, o, args.cmd, ms[sender], stdout, stderr)
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

func natsRun(ctx context.Context, o *options, c driving.NATSCommand, m natsMachine, stdout, stderr io.Writer) error {
	show := func(e model.NATSLine) {
		if o.asJSON {
			writeJSONLine(stdout, e)
		} else if e.Via != "presence" {
			fmt.Fprintln(stdout, natsLine(e))
		}
	}
	res, err := o.host().Services.NATS.Run(ctx, sshRemote(o, m), m.name, c, stderr, show)
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
		if o.asJSON {
			writeJSONLine(stdout, model.NATSLine{Event: "sent", From: res.Sender})
			return nil
		}
		to := "the topic " + c.Topic
		if c.Name == "send" {
			to = c.To
		}
		fmt.Fprintf(stdout, "sent as %s to %s (not confirmed: the machine may be offline)\n", res.Sender, to)
	case "request":
		if res.Answer == nil {
			return fmt.Errorf("no answer from %s", c.To)
		}
		if o.asJSON {
			writeJSONLine(stdout, *res.Answer)
			return nil
		}
		fmt.Fprintln(stdout, res.Answer.Text)
	case "peers":
		reportPeers(o, res, stdout)
	}
	return nil
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

func natsLine(e model.NATSLine) string {
	where := e.From
	if e.Topic != "" {
		where += " [" + e.Topic + "]"
	}
	if e.Type == "req" {
		where += " (request)"
	}
	return where + ": " + strings.ReplaceAll(e.Text, "\n", "\n    ")
}

func writeJSONLine(w io.Writer, e model.NATSLine) {
	b, _ := json.Marshal(e) // strings and booleans only
	fmt.Fprintf(w, "%s\n", b)
}
