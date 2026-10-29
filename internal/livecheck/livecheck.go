// Package livecheck serves the checks of the real price sources (internal/live): it tells a source that
// cannot be reached from one that answers but is no longer read as before. Only the second is a
// finding; the first says nothing about viper.
package livecheck

import (
	"context"
	"errors"
	"io"
	"net"
	"regexp"
)

// refused matches the answers that mean "not now, not you": a server that blocks the addresses of a CI
// runner, limits the rate, or has an outage.
var refused = regexp.MustCompile(`\bHTTP (403|429|5[0-9]{2})\b`)

// Inconclusive reports whether err says only that the source could not be reached: a network
// failure, a timeout, a body that was cut off, or a refusal (403, 429, 5xx). A plain end of data
// (io.EOF) is not: an empty answer is what a changed source can look like.
func Inconclusive(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &ne) ||
		refused.MatchString(err.Error())
}
