package cli_test

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"testing"
	"time"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
)

// TestHelperProcess is not a test: it is the program that the test below starts (the test binary itself), the way viper ui starts
// viper. It says that it is ready, waits for Ctrl-C and ends with 130 as viper does.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("VIPER_UI_HELPER") != "1" {
		return
	}
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	fmt.Println("ready") //nolint:forbidigo // the helper process talks to its parent by its output
	select {
	case <-c:
		fmt.Println("interrupted") //nolint:forbidigo // see above
		os.Exit(130)
	case <-time.After(30 * time.Second):
		fmt.Println("no interrupt came") //nolint:forbidigo // see above
		os.Exit(2)
	}
}

// The Interrupt of a process of viper ui is Ctrl-C for that process: on Linux and macOS a SIGINT, on Windows a CTRL_BREAK to its
// own process group. It must reach the program and end it in order, with the status of an interrupted viper (130).
func TestTheInterruptOfAProcessReachesItAsCtrlC(t *testing.T) {
	t.Setenv("VIPER_UI_HELPER", "1")
	p, err := cli.StartProcess(os.Args[0], "-test.run=^TestHelperProcess$")
	if err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(p.Output())
	line, err := r.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("first line %q (%v)", line, err)
	}
	if err := p.Interrupt(); err != nil {
		t.Fatalf("the interrupt could not be sent: %v", err)
	}
	rest, _ := io.ReadAll(r)
	if code := p.Wait(); code != 130 || !strings.Contains(string(rest), "interrupted") {
		t.Errorf("exit status %d, output %q: the program did not get Ctrl-C", code, rest)
	}
}
