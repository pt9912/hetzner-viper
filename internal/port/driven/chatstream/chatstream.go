// Package chatstream is the contract between the chat and the NATS server of a group: the connection of the operator and the stream
// it reads.
package chatstream

import (
	"context"
	"errors"
	"io"
)

// Conn is the connection of the operator to the NATS server of a group: Watch reads the stream (watch --all --query) into the
// writers until the context ends or the connection is gone; Dead is closed when the tunnel has ended (then Watch hangs: it does not
// notice); Close frees the tunnel.
type Conn interface {
	Watch(ctx context.Context, stdout, stderr io.Writer) error
	Dead() <-chan struct{}
	Close()
}

// Connector opens a connection for a group. An error that is ErrAuth means that the server does not know the key of the operator.
type Connector interface {
	Connect(ctx context.Context, group string) (Conn, error)
}

// ErrAuth is the reason of a connection that does not come up because the server does not know the key.
var ErrAuth = errors.New("the server does not know the key of the operator (Authorization Violation)")
