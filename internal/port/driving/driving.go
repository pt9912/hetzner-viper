// Package driving is the driving (primary) port: what the outside, the command line today, asks of
// viper. The use cases (internal/usecase) implement it; adapters (internal/adapter/driving) call
// it and know nothing of how it is done.
package driving

import (
	"context"
	"errors"
	"io"

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
}

// Down deletes the machine after the checks, with the same results as Up.
type Down interface {
	Down(ctx context.Context, c *model.Machine, force bool, lg runlog.Log, out, errOut io.Writer) (int, error)
}

// DownOptions are what the caller decides about one run of Down.
type DownOptions struct {
	// Ask shows a prompt and returns the answer line ("" at the end of the input).
	Ask func(prompt string) string
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
