package natsgroup_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/usecase/natsgroup"
)

// Machines that join at the same moment run natsconfig.sh at the same moment, each with the hash of the
// file it read. Exactly one may win, and the file must be what the winner sent: before the script wrote
// its temporary file under a name of its own, a loser could replace the file the winner had checked.
func TestConfigScriptLetsExactlyOneOfManyConcurrentWritersWin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the script runs on the server, in bash")
	}
	for _, tool := range []string{"flock", "sha256sum", "mktemp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("no " + tool)
		}
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "nats-server")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	empty := sha256.Sum256(nil)
	const writers = 8
	type result struct {
		out  string
		err  error
		body string
	}
	results := make([]result, writers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < writers; i++ {
		body := strings.Repeat(string(rune('a'+i))+"\n", 100000)
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command("bash", "-c", natsgroup.ConfigScript(), "nats", dir, "viper-nats-test-unit", "none")
			cmd.Env = append(os.Environ(), "VIPER_NATS_BIN="+fake)
			cmd.Stdin = strings.NewReader(body)
			<-start
			out, err := cmd.CombinedOutput()
			results[i] = result{string(out), err, body}
		}()
	}
	close(start)
	wg.Wait()
	_ = empty // the script is told "none": the file does not exist yet
	var winner *result
	for i := range results {
		r := &results[i]
		if r.err == nil {
			if winner != nil {
				t.Fatalf("two writers won:\n%s\n%s", winner.out, r.out)
			}
			winner = r
			continue
		}
		if !strings.Contains(r.out, "changed meanwhile") {
			t.Errorf("a loser must say the file changed: %v\n%s", r.err, r.out)
		}
	}
	if winner == nil {
		t.Fatal("nobody won")
	}
	got, err := os.ReadFile(filepath.Join(dir, "server.conf"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(got)
	want := sha256.Sum256([]byte(winner.body))
	if hex.EncodeToString(sum[:]) != hex.EncodeToString(want[:]) {
		t.Error("server.conf is not what the winner sent: a loser replaced it")
	}
	left, _ := filepath.Glob(filepath.Join(dir, "server.conf.*"))
	if len(left) != 0 {
		t.Errorf("temporary files are left behind: %v", left)
	}
}
