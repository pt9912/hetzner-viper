package natsserver_test

import (
	"os"
	"regexp"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/natsserver"
)

// The image of the Kubernetes provider (image/Dockerfile) carries the same nats-server and nats releases as a VM gets
// from viper: these tests keep the versions and checksums in the Dockerfile equal to the ones in this package.
func TestTheImagePinsTheSameNATSReleasesAsTheCode(t *testing.T) {
	b, err := os.ReadFile("../../image/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	arg := func(name string) string {
		m := regexp.MustCompile(`(?m)^ARG ` + name + `=(\S+)$`).FindSubmatch(b)
		if m == nil {
			t.Fatalf("image/Dockerfile has no ARG %s", name)
		}
		return string(m[1])
	}
	for _, c := range []struct{ arch, uname string }{{"AMD64", "x86_64"}, {"ARM64", "aarch64"}} {
		srv, err := natsserver.ReleaseFor(c.uname)
		if err != nil {
			t.Fatal(err)
		}
		cli, err := natsserver.CLIReleaseFor(c.uname)
		if err != nil {
			t.Fatal(err)
		}
		if got := arg("NATS_SERVER_SHA256_" + c.arch); got != srv.SHA256 {
			t.Errorf("nats-server %s: Dockerfile %s, code %s", c.arch, got, srv.SHA256)
		}
		if got := arg("NATS_CLI_SHA256_" + c.arch); got != cli.SHA256 {
			t.Errorf("nats %s: Dockerfile %s, code %s", c.arch, got, cli.SHA256)
		}
	}
	if got := arg("NATS_SERVER_VERSION"); got != natsserver.Version {
		t.Errorf("nats-server version: Dockerfile %s, code %s", got, natsserver.Version)
	}
	if got := arg("NATS_CLI_VERSION"); got != natsserver.CLIVersion {
		t.Errorf("nats version: Dockerfile %s, code %s", got, natsserver.CLIVersion)
	}
}
