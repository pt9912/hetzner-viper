package model

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,63}$`)

// CheckDomain reports a DNS_DOMAIN that is not a plain lowercase domain.
func CheckDomain(d string) error {
	if !domainRe.MatchString(d) {
		return fmt.Errorf("DNS_DOMAIN must be a domain like example.com (lowercase, no scheme, no trailing dot): %s", d)
	}
	return nil
}

// KeyPath is the login key: SSH_KEY (default ~/.ssh/hetzner-build) with ~ expanded. A relative
// path is relative to the settings directory, where the scripts ran (they changed into it).
func KeyPath(setting, dir, home string) string {
	if setting == "" {
		return filepath.Join(home, ".ssh", "hetzner-build")
	}
	p := ExpandHome(setting, home)
	if !filepath.IsAbs(p) {
		if abs, err := filepath.Abs(filepath.Join(dir, p)); err == nil {
			p = abs
		}
	}
	return filepath.FromSlash(p) // a ~ expanded in .env.local leaves "home/rest"
}

// ExpandHome turns a leading ~ into the home directory (${SSH_KEY/#\~/$HOME}).
func ExpandHome(p, home string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || filepath.Separator == '\\' && strings.HasPrefix(p, `~\`) {
		return home + filepath.FromSlash(p[1:])
	}
	return p
}
