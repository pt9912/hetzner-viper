//go:build windows

package cli_test

import (
	"os"
	"testing"
)

func killProcess(t *testing.T, pid int) {
	t.Helper()
	p, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
}
