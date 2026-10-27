//go:build e2e

package app_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	runlogfile "github.com/pt9912/hetzner-viper/internal/adapter/driven/runlog"
	"github.com/pt9912/hetzner-viper/internal/adapter/driven/settingsfile"
	"github.com/pt9912/hetzner-viper/internal/app"
	"github.com/pt9912/hetzner-viper/internal/port/driven/settings"
	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

// noDNS stands in for Cloudflare: the test has no zone and must not touch one.
type noDNS struct{}

func (noDNS) Set(_ context.Context, host, ip string) (string, error) {
	return "DNS skipped in the e2e test: " + host + " -> " + ip, nil
}
func (noDNS) Delete(context.Context, string, string) (string, error) {
	return "DNS skipped in the e2e test", nil
}
func (noDNS) Check(context.Context, string, string) ([]string, bool, error) {
	return []string{"ok    DNS skipped in the e2e test"}, true, nil
}

// logWriter writes what the use cases print to the test log, line by line, as it comes.
type logWriter struct {
	t  *testing.T
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.b.Write(p)
	for {
		line, err := w.b.ReadString('\n')
		if err != nil {
			w.b.WriteString(line)
			break
		}
		w.t.Log(strings.TrimRight(line, "\n"))
	}
	return len(p), nil
}

func run(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.CommandContext(context.Background(), name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func publicIP(t *testing.T) string {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://checkip.amazonaws.com", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return strings.TrimSpace(string(b))
}

// TestE2EUpAndDown sets a machine up on a real AWS account and deletes it again, with the real use
// cases and adapters (preflight, up with cloud-init, ssh, down). Only DNS is a stub. SSH is open to
// the address of this computer only. It takes about ten minutes and costs a few cents. Run it with
//
//	VIPER_E2E_AWS=<profile> go test -tags e2e -run TestE2EUpAndDown -v -timeout 40m ./internal/app/
//
// VIPER_E2E_TYPE picks the instance type (default t3.medium; t4g.small tests an ARM machine).
func TestE2EUpAndDown(t *testing.T) {
	profile := os.Getenv("VIPER_E2E_AWS")
	if profile == "" {
		t.Skip("set VIPER_E2E_AWS=<profile> to run against a real account")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
	defer cancel()

	// a home of its own: the login key, the host CA and the ssh config are made here
	home, dir := t.TempDir(), t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	run(t, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "viper e2e login", "-f", filepath.Join(sshDir, "hetzner-build"))
	ca := filepath.Join(sshDir, "hetzner_claude_host_ca")
	run(t, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "viper e2e host CA", "-f", ca)
	caPub, err := os.ReadFile(ca + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	known := fmt.Sprintf("@cert-authority *.example.test %s\n", strings.TrimSpace(string(caPub)))
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts_hetzner_claude_ca"), []byte(known), 0o600); err != nil {
		t.Fatal(err)
	}

	size := os.Getenv("VIPER_E2E_TYPE")
	if size == "" {
		size = "t3.medium"
	}
	prefix := fmt.Sprintf("viper-e2e-%d", time.Now().Unix())
	cidr := publicIP(t) + "/32"
	t.Logf("machine %s-web (%s), ssh open to %s only", prefix, size, cidr)
	envFile := strings.Join([]string{
		"DNS_DOMAIN=example.test", "PROVIDER=aws", "SERVER_PREFIX=" + prefix, "AWS_PROFILE=" + profile,
		"SERVER_TYPE=" + size, "SSH_ALLOWED_CIDRS=" + cidr, "VIPER_HOMEBREW=0",
		"SSH_KEY=" + filepath.Join(sshDir, "hetzner-build"),
		"CLOUDFLARE_API_TOKEN=unused", "CLOUDFLARE_ZONE_ID=unused", "",
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte(envFile), 0o600); err != nil {
		t.Fatal(err)
	}
	env := settings.Env{Home: home, Lookup: os.LookupEnv}
	m, err := settingsfile.Files{}.Load(dir, "web", env, true)
	if err != nil {
		t.Fatal(err)
	}

	// whatever happens: no instance, key pair or security group is left behind
	t.Cleanup(func() {
		cctx := context.Background()
		p := app.NewProvider(m)
		if srv, err := p.ServerByName(cctx, m.ServerName); err == nil && srv != nil {
			if err := p.DeleteServer(cctx, srv.ID); err != nil {
				t.Errorf("LEFT OVER instance %s: %v", srv.ID, err)
			}
		}
		cfg, err := awsconfig.LoadDefaultConfig(cctx, awsconfig.WithRegion(m.ServerLocation), awsconfig.WithSharedConfigProfile(profile))
		if err != nil {
			t.Errorf("cleanup: %v", err)
			return
		}
		raw := ec2.NewFromConfig(cfg)
		if _, err := raw.DeleteKeyPair(cctx, &ec2.DeleteKeyPairInput{KeyName: &m.KeyName}); err != nil {
			t.Errorf("LEFT OVER key pair %s: %v", m.KeyName, err)
		}
		sg, err := raw.DescribeSecurityGroups(cctx, &ec2.DescribeSecurityGroupsInput{GroupNames: []string{m.FirewallName}})
		if err == nil && len(sg.SecurityGroups) == 1 {
			if _, err := raw.DeleteSecurityGroup(cctx, &ec2.DeleteSecurityGroupInput{GroupId: sg.SecurityGroups[0].GroupId}); err != nil {
				t.Errorf("LEFT OVER security group %s: %v", m.FirewallName, err)
			}
		}
	})

	out := &logWriter{t: t}

	// up
	up := app.Up(m, driving.UpOptions{NoCode: true})
	up.Deps.DNS = noDNS{}
	up.Deps.Preflight = func(ctx context.Context, repo string, o, e io.Writer) int {
		p := app.Preflight(m)
		p.Deps.DNS = noDNS{}
		return p.Preflight(ctx, m, repo, o, e)
	}
	lg, err := runlogfile.Open(m.StateDir, m.ServerName, "up", out, out)
	if err != nil {
		t.Fatal(err)
	}
	code, err := up.Up(ctx, m, "", lg, lg.Out(), lg.Err())
	lg.Close(err, code)
	if code != 0 || err != nil {
		t.Fatalf("viper up: exit %d, %v", code, err)
	}
	srv, err := app.NewProvider(m).ServerByName(ctx, m.ServerName)
	if err != nil || srv == nil || srv.IPv4 == "" {
		t.Fatalf("the server is not there after up: %+v %v", srv, err)
	}
	t.Logf("up: %s at %s", srv.ID, srv.IPv4)

	// down
	down := app.Down(m, driving.DownOptions{Ask: func(string) string { return "DELETE" }})
	down.Deps.DNS = noDNS{}
	lg, err = runlogfile.Open(m.StateDir, m.ServerName, "down", out, out)
	if err != nil {
		t.Fatal(err)
	}
	code, err = down.Down(ctx, m, false, lg, lg.Out(), lg.Err())
	lg.Close(err, code)
	if code != 0 || err != nil {
		t.Fatalf("viper down: exit %d, %v", code, err)
	}
	if srv, err := app.NewProvider(m).ServerByName(ctx, m.ServerName); err != nil || srv != nil {
		t.Errorf("the server is still there after down: %+v %v", srv, err)
	}
}
