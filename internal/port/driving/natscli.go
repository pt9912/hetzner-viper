package driving

import (
	"context"
	"io"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
)

// NATSRemote runs a program on a machine of a NATS group and passes its stdout and stderr on. stdin is
// the text of the message. It must not repeat the program after a lost connection: a send would then
// arrive twice. The program is given as words; the implementation quotes them for the shell.
type NATSRemote func(ctx context.Context, machine, stdin string, stdout, stderr io.Writer, words ...string) error

// NATSCommand is what viper nats was asked for (docs/plan/nats-cli.md).
type NATSCommand struct {
	Name    string        // peers, send, broadcast, request, listen
	To      string        // send, request
	Topic   string        // broadcast: one topic; listen: a filter, optional, several separated by commas
	Text    string        // send, broadcast, request
	Timeout time.Duration // request (default 30 seconds); peers: how long to listen
	Query   bool          // peers: ask the others to say that they are there
	All     bool          // listen: also the messages between the other machines
}

// NATSPeer is a machine that said that it is there.
type NATSPeer struct{ Name string }

// NATSResult is what a command produced.
type NATSResult struct {
	Sender string          // the machine that spoke
	Peers  []NATSPeer      // peers
	Answer *model.NATSLine // request
	Sent   bool            // send, broadcast
}

// NATSCLI speaks to a NATS group as one of its machines.
type NATSCLI interface {
	// Run runs the command on the machine and calls show for every message of listen. stderr gets what
	// the program on the machine prints to its own.
	Run(ctx context.Context, remote NATSRemote, machine string, c NATSCommand, stderr io.Writer, show func(model.NATSLine)) (NATSResult, error)
	// ChooseAccess is the machine that speaks: from if it is one of machines, else the first machine,
	// in the order of machines, that is not to and that reachable accepts.
	ChooseAccess(machines []string, from, to string, reachable func(string) bool) (string, error)
}
