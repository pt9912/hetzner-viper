package start_test

import (
	"net"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/usecase/start"
)

// serveInfo answers every connection with the INFO line a NATS server sends first, until the test ends.
func serveInfo(t *testing.T, info string) (ip, port string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no local listener")
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte(info + "\r\n"))
			_ = c.Close()
		}
	}()
	h, p, _ := net.SplitHostPort(l.Addr().String())
	return h, p
}

func runBash(t *testing.T, script string, args ...string) (string, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the script runs on the server, in bash")
	}
	out, err := exec.Command("bash", append([]string{"-c", script, "x"}, args...)...).CombinedOutput()
	return string(out), err
}

// JetStream is off, except for a group with an object store, where it must be on.
func TestTheServerCheckWantsJetStreamOffOrOnAsTheGroupSaysIt(t *testing.T) {
	const base = `INFO {"server_name":"viper-dev","tls_required":true,"auth_required":true`
	for name, tc := range map[string]struct {
		info, objects, want string
		ok                  bool
	}{
		"off, as always":        {base + `}`, "", "TLS and login are required", true},
		"off, said off":         {base + `}`, "off", "TLS and login are required", true},
		"on, but must be off":   {base + `,"jetstream":true}`, "off", "JetStream is on, it must not be", false},
		"on by default is off":  {base + `,"jetstream":true}`, "", "JetStream is on, it must not be", false},
		"on, as the group says": {base + `,"jetstream":true}`, "on", "TLS and login are required", true},
		"off, but must be on":   {base + `}`, "on", "JetStream is off, the group has an object store", false},
		"on with spaces":        {base + `,"jetstream": true}`, "on", "TLS and login are required", true},
	} {
		ip, port := serveInfo(t, tc.info)
		args := []string{ip, port}
		if tc.objects != "" {
			args = append(args, tc.objects)
		}
		out, err := runBash(t, start.NatsCheckScript(), args...)
		if (err == nil) != tc.ok || !strings.Contains(out, tc.want) {
			t.Errorf("%s: %v\n%s", name, err, out)
		}
	}
}

// The room for the object store is looked for in the nearest directory that exists.
func TestTheDiskCheckSaysWhetherTheObjectStoreFits(t *testing.T) {
	dir := t.TempDir() + "/not/made/yet"
	if out, err := runBash(t, start.NatsDiskScript(), dir, "1048576"); err != nil || !strings.Contains(out, "MiB free at") {
		t.Errorf("1 MiB fits: %v\n%s", err, out)
	}
	out, err := runBash(t, start.NatsDiskScript(), dir, "999999999999999999")
	if err == nil || !strings.Contains(out, "the object store needs") || !strings.Contains(out, "NATS_OBJECTS_MAX") {
		t.Errorf("an exabyte does not fit: %v\n%s", err, out)
	}
	if _, err := runBash(t, start.NatsDiskScript(), dir, "lots"); err == nil {
		t.Error("a size that is no number was accepted")
	}
}
