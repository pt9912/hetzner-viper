// Package system is the port to the computer viper runs on: programs, time and the host-key
// generator. Adapters (internal/adapter/driven/system) run the real ones; tests give fakes.
package system

import (
	"context"
	"io"
	"time"
)

// Runner runs a program and returns what it printed (stdin may be empty).
type Runner func(ctx context.Context, stdin, name string, args ...string) (stdout, stderr string, err error)

// Streamer runs a program with its output going to the user as it comes.
type Streamer func(ctx context.Context, stdin string, stdout, stderr io.Writer, name string, args ...string) error

// Keygen runs ssh-keygen (the host key of a new server, signed by the host CA).
type Keygen func(name string, args ...string) error

// Sleeper waits.
type Sleeper func(time.Duration)

// Clock tells the time.
type Clock func() time.Time
