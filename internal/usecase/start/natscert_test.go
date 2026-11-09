package start_test

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/usecase/start"
)

// runCert runs the real natscert.sh in a temporary directory and returns the files it made.
func runCert(t *testing.T, dir, ip string) (out string, err error) {
	t.Helper()
	cmd := exec.Command("bash", "-c", start.NatsCertScript(), "nats", dir, ip, "viper-nats-test")
	b, err := cmd.CombinedOutput()
	return string(b), err
}

func loadCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		t.Fatalf("%s is no PEM", path)
	}
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTheServerCertificateIsALeafThatClientsOfAnyLanguageAccept(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the script runs on the server, in bash with openssl")
	}
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("no openssl")
	}
	dir := filepath.Join(t.TempDir(), "nats")
	out, err := runCert(t, dir, "10.115.0.2")
	if err != nil || !strings.Contains(out, "nats cert: created") {
		t.Fatalf("%v\n%s", err, out)
	}
	crt := loadCert(t, filepath.Join(dir, "server.crt"))
	// not a CA: rustls refuses a CA certificate as a server certificate (CaUsedAsEndEntity)
	if crt.IsCA || !crt.BasicConstraintsValid {
		t.Errorf("must carry basicConstraints CA:FALSE: IsCA=%v valid=%v", crt.IsCA, crt.BasicConstraintsValid)
	}
	if crt.KeyUsage&x509.KeyUsageDigitalSignature == 0 || crt.KeyUsage&x509.KeyUsageCertSign != 0 {
		t.Errorf("key usage %v", crt.KeyUsage)
	}
	if len(crt.ExtKeyUsage) != 1 || crt.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Errorf("extended key usage %v", crt.ExtKeyUsage)
	}
	if len(crt.IPAddresses) != 1 || crt.IPAddresses[0].String() != "10.115.0.2" {
		t.Errorf("SAN %v", crt.IPAddresses)
	}
	if d := crt.NotAfter.Sub(crt.NotBefore); d < 30*24*time.Hour || d > 32*24*time.Hour {
		t.Errorf("valid for %v", d)
	}
	// a client that trusts exactly this certificate accepts it for the server's address
	pool := x509.NewCertPool()
	pool.AddCert(crt)
	if _, err := crt.Verify(x509.VerifyOptions{Roots: pool, DNSName: "10.115.0.2", KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Errorf("verify: %v", err)
	}
	for f, want := range map[string]os.FileMode{"server.key": 0o600, "server.crt": 0o644} {
		if st, err := os.Stat(filepath.Join(dir, f)); err != nil || st.Mode().Perm() != want {
			t.Errorf("%s: %v %v", f, st, err)
		}
	}
}

func TestAnExistingCertificateIsKeptAndNeverReplacedBehindTheClientsBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash and openssl")
	}
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("no openssl")
	}
	dir := filepath.Join(t.TempDir(), "nats")
	if out, err := runCert(t, dir, "10.115.0.2"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "server.crt"))
	out, err := runCert(t, dir, "10.115.0.2")
	after, _ := os.ReadFile(filepath.Join(dir, "server.crt"))
	if err != nil || !strings.Contains(out, "nats cert: kept") || string(before) != string(after) {
		t.Errorf("kept: %v\n%s", err, out)
	}
	// the address changed: the group ends, the certificate is not made again
	if out, err := runCert(t, dir, "10.115.0.9"); err == nil || !strings.Contains(out, "not for 10.115.0.9") {
		t.Errorf("another address must be refused: %v\n%s", err, out)
	}
}
