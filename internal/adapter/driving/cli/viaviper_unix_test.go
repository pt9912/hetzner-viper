//go:build !windows

package cli_test

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// the server and its child as processes: the child must go when the server is killed.
func TestTheChildEndsWhenTheServerIsKilled(t *testing.T) {
	v := newViaWorld(t)
	t.Setenv("VIPER_TEST_AS", asMCP)
	cmd := exec.Command(v.viper, "--via-viper", "dev", "--viper", v.viper, "--dir", v.dir)
	cmd.Env = append(os.Environ(), "VIPER_TEST_AS="+asMCP)
	in, _ := cmd.StdinPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait(); _ = in.Close() }()
	pid := 0
	sc := bufio.NewScanner(stderr)
	deadline := time.Now().Add(20 * time.Second)
	for pid == 0 && time.Now().Before(deadline) && sc.Scan() {
		if _, rest, ok := strings.Cut(sc.Text(), "viper nats tunnel pid "); ok {
			pid, _ = strconv.Atoi(strings.TrimSuffix(rest, ")"))
		}
	}
	if pid == 0 {
		t.Fatal("the server did not say which child it started")
	}
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("the child is not running: %v", err)
	}
	_ = cmd.Process.Kill() // SIGKILL: nothing is cleaned up by the server
	_, _ = cmd.Process.Wait()
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if gone(pid) {
			return
		}
	}
	t.Errorf("the child %d is still running after its parent was killed", pid)
}

func killProcess(t *testing.T, pid int) {
	t.Helper()
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
}

// gone tells whether the process has ended: one that is a zombie (nobody has collected it, as in a container without an init) has.
func gone(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return true
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	_, rest, ok := strings.Cut(string(b), ") ")
	return ok && strings.HasPrefix(rest, "Z")
}
