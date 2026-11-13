// Package driving is the driving (primary) port: what the outside, the command line today, asks of
// viper. The use cases (internal/usecase) implement it; adapters (internal/adapter/driving) call
// it and know nothing of how it is done.
package driving

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
	"github.com/pt9912/hetzner-viper/internal/port/driven/runlog"
	"github.com/pt9912/hetzner-viper/internal/port/driven/system"
)

// ErrShown is returned when a run failed after its reason was shown: the run is logged as FAILED.
var ErrShown = errors.New("failed")

// Up creates or reuses the machine and sets it up. It returns the exit code; a non-nil error is a
// run that failed (ErrShown: the reason was already shown) and is to be logged as FAILED.
type Up interface {
	Up(ctx context.Context, c *model.Machine, repoArg string, lg runlog.Log, out, errOut io.Writer) (int, error)
}

// UpOptions are what the caller decides about one run of Up.
type UpOptions struct {
	NoCode bool // do not open VS Code
	// Sleep waits between two tries; the caller makes it end with an interruption.
	Sleep system.Sleeper
	// OnServerCreated is called once, as soon as the server of the machine exists at the provider and
	// has its place in the NATS group (the role and the private network are set): a new server right
	// after it was made, a server that was there without a group once it has joined. The group
	// commands start the other machines of a new group then. It is not called if the run ends before.
	OnServerCreated func()
	// SkipPreflight: the caller has run the checks already (for all machines of a group).
	SkipPreflight bool
	// ServerWait is how long a client of a NATS group waits for the server to be ready; 0: 5 minutes.
	ServerWait time.Duration
}

// Down deletes the machine after the checks, with the same results as Up.
type Down interface {
	Down(ctx context.Context, c *model.Machine, force bool, lg runlog.Log, out, errOut io.Writer) (int, error)
}

// GroupRoler is implemented by a Down that can say what its machine is in its NATS group, read from
// the labels at the provider (not from the settings). The group commands stop the clients first and the
// server last.
type GroupRoler interface {
	// GroupRole returns the role of the machine and its group; ok is false if it has none (or does not exist).
	GroupRole(ctx context.Context, c *model.Machine) (role model.NATSRole, group string, ok bool, err error)
}

// DownOptions are what the caller decides about one run of Down.
type DownOptions struct {
	// Ask shows a prompt and returns the answer line ("" at the end of the input).
	Ask func(prompt string) string
	// Registry says what happens to a registry that viper made for the machines when the last one is deleted: "delete",
	// "keep", or "" to ask. The registry holds the image for other clusters, so it is the user's to decide.
	Registry string
}

// Check runs the read-only checks of a running machine and returns the number of problems. A
// non-nil error means the checks could not run: the reason was printed.
type Check interface {
	Check(ctx context.Context, c *model.Machine, out, errOut io.Writer) (int, error)
}

// Preflight runs the read-only checks before a machine is created and returns the number of
// problems.
type Preflight interface {
	Preflight(ctx context.Context, c *model.Machine, repoArg string, out, errOut io.Writer) int
}

// Find shows the server types of the providers that have at least what a machine needs, with
// their prices, and returns how many it showed.
type Find interface {
	Find(ctx context.Context, need model.Need, out, errOut io.Writer) int
}
