// Package natscli is viper nats (docs/plan/nats-cli.md, stage 1): it sends to a NATS group and reads
// from it as one of the machines of the group, over ssh. The machine runs viper-nats-mcp with a
// subcommand (watch, send, broadcast, request) that prints one JSON line per event; the text of a
// message goes over stdin and is never part of a command line. viper does not read the output of
// the nats tool: the text of a message can look like a line of its own there.
package natscli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/natsmcp"
	"github.com/pt9912/hetzner-viper/internal/natsserver"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

// MaxTimeout is the longest time a request waits (as in the tool request).
const MaxTimeout = 120 * time.Second

// PeersWindow is how long peers listens by default: a machine says that it is there every 30 seconds.
const PeersWindow = 35 * time.Second

// QueryWindow is how long peers listens after it asked the others to answer (they wait up to 2 seconds, and answer a query only every 5).
const QueryWindow = 8 * time.Second

// Validate checks a command before anything is run. The names are those of the machines and topics of the group.
func Validate(c driving.NATSCommand, from string) error {
	if err := validateTarget(c, from); err != nil {
		return err
	}
	if (c.Name == "send" || c.Name == "broadcast" || c.Name == "request") && c.Text == "" {
		return errors.New("the text is empty")
	}
	if c.Name == "request" && c.Timeout != 0 && (c.Timeout < time.Second || c.Timeout > MaxTimeout) {
		return errors.New("the timeout must be between 1 and 120 seconds")
	}
	return nil
}

// validateTarget checks the names the command is about.
func validateTarget(c driving.NATSCommand, from string) error {
	switch c.Name {
	case "peers":
		return nil
	case "listen":
		if c.Topic != "" && len(topicList(c.Topic)) == 0 {
			return errors.New("topic: no name in the list")
		}
		for _, t := range topicList(c.Topic) {
			if err := checkName("topic", t); err != nil {
				return err
			}
		}
		return nil
	case "send", "request":
		if err := checkName("machine", c.To); err != nil {
			return err
		}
		if c.To == from {
			return fmt.Errorf("%s is the sender itself", c.To)
		}
		return nil
	case "broadcast":
		if len(topicList(c.Topic)) > 1 {
			return errors.New("broadcast takes one topic: a message goes to exactly one")
		}
		return checkName("topic", c.Topic)
	}
	return fmt.Errorf("unknown command %q", c.Name)
}

// topicList splits the topics of listen (--topic given more than once, or a list with commas); none: all.
func topicList(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func checkName(what, s string) error { return model.CheckNATSName(what, s) }

// Words are the words of the program that runs on the machine for the command. They contain only
// validated names and numbers; the text is not among them.
func Words(c driving.NATSCommand) []string {
	w := []string{natsserver.MCPBinary}
	switch c.Name {
	case "send":
		return append(w, "send", "--to", c.To)
	case "broadcast":
		return append(w, "broadcast", "--topic", c.Topic)
	case "request":
		t := c.Timeout
		if t == 0 {
			t = 30 * time.Second
		}
		return append(w, "request", "--to", c.To, "--timeout", t.String())
	case "peers":
		d := c.Timeout
		if d == 0 {
			d = PeersWindow
			if c.Query {
				d = QueryWindow
			}
		}
		w = append(w, "watch", "--for", d.String())
		if c.Query {
			w = append(w, "--query")
		}
		return w
	}
	w = append(w, "watch") // listen
	if c.All {
		w = append(w, "--all")
	}
	return w
}

// Event is one line the machine printed, checked.
type Event = model.NATSLine

// ReadEvents reads the JSON lines of a subcommand. A line that is not valid JSON, has an unknown
// event or an invalid name is an error: the machine does not print such lines, so something else
// is writing to the output. The text is cleaned once more (the machine did it already).
func ReadEvents(r io.Reader, handle func(Event) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 256<<10)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Event
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&e); err != nil {
			return fmt.Errorf("the machine printed a line that is not an event: %.80q", line)
		}
		if err := checkEvent(e); err != nil {
			return err
		}
		e.Text = natsmcp.Clean(e.Text)
		if err := handle(e); err != nil {
			return err
		}
	}
	return sc.Err()
}

func checkEvent(e Event) error {
	switch e.Event {
	case "ready", "sent", "answer":
	case "message":
		if e.Via != "out" && e.Via != "pub" && e.Via != "presence" {
			return fmt.Errorf("an event with the route %.20q", e.Via)
		}
	default:
		return fmt.Errorf("an event of the kind %.20q", e.Event)
	}
	for _, n := range []string{e.Me, e.From, e.To} {
		if n != "" && !natsmcp.ValidName(n) {
			return fmt.Errorf("an event with the name %.20q", n)
		}
	}
	if e.Topic != "" && !natsmcp.ValidName(e.Topic) {
		return fmt.Errorf("an event with the topic %.20q", e.Topic)
	}
	return nil
}

// ChooseAccess is the machine that speaks for the user: from if given (it must be a machine of the
// group), else the first machine of the group, in the order of the group, that is not to and that
// reachable accepts.
func (Service) ChooseAccess(machines []string, from, to string, reachable func(string) bool) (string, error) {
	if from != "" {
		for _, m := range machines {
			if m == from {
				return from, nil
			}
		}
		return "", fmt.Errorf("%s is not a machine of the group", from)
	}
	for _, m := range machines {
		if m != to && reachable(m) {
			return m, nil
		}
	}
	return "", errors.New("no machine of the group can be reached")
}

// Service is the use case viper nats; it implements driving.NATSCLI.
type Service struct{}

// Run runs the command on the machine and calls show for every message (listen) or collects the result.
// stderr gets what the machine prints to its own.
func (Service) Run(ctx context.Context, remote driving.NATSRemote, machine string, c driving.NATSCommand, stderr io.Writer, show func(model.NATSLine)) (driving.NATSResult, error) {
	if err := Validate(c, machine); err != nil {
		return driving.NATSResult{}, err
	}
	res := driving.NATSResult{Sender: machine}
	pr, pw := io.Pipe()
	errc := make(chan error, 1)
	go func() {
		err := remote(ctx, machine, c.Text, pw, stderr, Words(c)...)
		_ = pw.CloseWithError(err) // ends the reader with the error of the program, if any
		errc <- err
	}()
	collect := collector{c: c, res: &res, show: show, seen: map[string]bool{}}
	readErr := ReadEvents(pr, collect.add)
	_ = pr.Close()
	runErr := <-errc
	switch {
	case readErr != nil && !errors.Is(readErr, runErr):
		return res, readErr
	case runErr != nil && ctx.Err() != nil && (c.Name == "listen" || c.Name == "peers"):
		return res, nil // interrupted or the time is up
	}
	return res, runErr
}

// collector files the events of a run into its result.
type collector struct {
	c    driving.NATSCommand
	res  *driving.NATSResult
	show func(Event)
	seen map[string]bool
}

func (k *collector) add(e Event) error {
	switch {
	case e.Event == "answer":
		k.res.Answer = &e
	case e.Event == "sent":
		k.res.Sent = true
	case e.Event != "message":
	case k.c.Name == "peers" && e.Via == "presence" && e.State != "bye" && !k.seen[e.From]:
		k.seen[e.From] = true
		k.res.Peers = append(k.res.Peers, driving.NATSPeer{Name: e.From})
	case k.c.Name == "listen" && k.show != nil && (k.c.Topic == "" || slices.Contains(topicList(k.c.Topic), e.Topic)):
		k.show(e)
	}
	return nil
}
