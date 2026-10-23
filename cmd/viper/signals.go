package main

import (
	"context"
	"os/signal"
	"syscall"
)

// notifyContext is cancelled by the first Ctrl-C, SIGTERM or SIGHUP. Then the default behavior is
// restored, so that a second Ctrl-C ends the program at once, even if cleaning up hangs.
func notifyContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-ctx.Done()
		stop()
	}()
	return ctx, stop
}
