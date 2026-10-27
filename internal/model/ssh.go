package model

import "regexp"

var (
	hostKeyRe = regexp.MustCompile(`(?i)host key verification failed|remote host identification has changed|host key for .* has changed|no .* host key is known|certificate invalid`)
	authRe    = regexp.MustCompile(`(?i)permission denied`)
)

// SSHFailureKind says why ssh failed, from its error output: "hostkey" if it rejected the
// server's identity, "auth" if the server rejected our key, else "network".
func SSHFailureKind(stderr string) string {
	switch {
	case hostKeyRe.MatchString(stderr):
		return "hostkey"
	case authRe.MatchString(stderr):
		return "auth"
	}
	return "network"
}
