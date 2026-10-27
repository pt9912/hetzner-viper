// Package resolver asks the name service of this computer: the addresses of a host and the
// resolver it uses.
package resolver

import (
	"context"
	"net"
	"os"
	"strings"
)

// Nameserver is the first resolver in /etc/resolv.conf, "" if there is none.
func Nameserver() string {
	b, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "nameserver" {
			return f[1]
		}
	}
	return ""
}

// LookupIPv4 is the IPv4 addresses of a host as this computer resolves it.
func LookupIPv4(ctx context.Context, host string) ([]string, error) {
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	out := make([]string, len(ips))
	for i, ip := range ips {
		out[i] = ip.String()
	}
	return out, err
}
