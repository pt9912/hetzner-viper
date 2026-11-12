package cloud

import "context"

// Starter is what a provider adds whose machines can stop without being deleted (a container): viper up on a machine that
// exists asks it to be running and whole before it goes on. It is a capability apart from Provider (ask with As).
type Starter interface {
	// EnsureRunning starts the machine if it is stopped and repairs what a plain restart of it left broken.
	EnsureRunning(ctx context.Context, name string) error
}
