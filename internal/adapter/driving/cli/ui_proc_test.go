//go:build !windows

package cli_test

import (
	"io"
	"strings"
	"testing"

	"github.com/pt9912/hetzner-viper/internal/adapter/driving/cli"
)

func TestAProcessReportsItsExitStatusAndItsOutputInOneStream(t *testing.T) {
	for name, tc := range map[string]struct {
		script string
		code   int
	}{
		"success":      {"echo out; echo err >&2; exit 0", 0},
		"failure":      {"echo bad >&2; exit 3", 3},
		"background":   {"echo hi; (sleep 8 &) ; exit 0", 0}, // a child keeps the pipe: the status is still the one of the process
		"killed by it": {"kill -9 $$", 137},
	} {
		t.Run(name, func(t *testing.T) {
			p, err := cli.StartProcess("sh", "-c", tc.script)
			if err != nil {
				t.Fatal(err)
			}
			out, _ := io.ReadAll(p.Output())
			code := p.Wait()
			if name != "killed by it" && code != tc.code || name == "killed by it" && code == 0 {
				t.Errorf("exit status %d, want %d (output %q)", code, tc.code, out)
			}
			if name == "success" && (!strings.Contains(string(out), "out") || !strings.Contains(string(out), "err")) {
				t.Errorf("output %q", out)
			}
		})
	}
}

func TestTheStandardInputOfAProcessStaysOpenForAnswers(t *testing.T) {
	p, err := cli.StartProcess("sh", "-c", `printf 'Type DELETE: '; read a; echo "got:$a"`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(p.Input(), "DELETE\n"); err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(p.Output())
	if code := p.Wait(); code != 0 || !strings.Contains(string(out), "got:DELETE") {
		t.Errorf("%d %q", code, out)
	}
}

// A program that keeps the pipe open after viper itself was killed by a signal: the status is the one of a signal (130), not -1.
func TestAProcessKilledBySignalWhileAChildHoldsThePipe(t *testing.T) {
	p, err := cli.StartProcess("sh", "-c", "(sleep 8 &); kill -9 $$")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(p.Output())
	if code := p.Wait(); code != 130 {
		t.Errorf("exit status %d", code)
	}
}
