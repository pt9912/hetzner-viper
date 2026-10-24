package runlog_test

import (
	"fmt"
	"github.com/pt9912/hetzner-viper/internal/runlog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func shown(t *testing.T, state, server string, full bool) string {
	t.Helper()
	var b strings.Builder
	if err := runlog.Show(state, server, full, &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestShowEventsOfAllMachinesOrOne(t *testing.T) {
	state := t.TempDir()
	if got := shown(t, state, "", false); got != "no events yet\n" {
		t.Errorf("%q", got)
	}
	var all strings.Builder
	for i := 0; i < 60; i++ {
		name := "claude-web"
		if i%2 == 1 {
			name = "claude-api"
		}
		fmt.Fprintf(&all, "2026-10-21T10:00:%02d+02:00 %-24s up        event %d\n", i, name, i)
	}
	if err := os.WriteFile(filepath.Join(state, "events.log"), []byte(all.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	got := shown(t, state, "", false)
	if lines := strings.Count(got, "\n"); lines != 50 || !strings.Contains(got, "event 59\n") || strings.Contains(got, "event 9\n") {
		t.Errorf("last 50 lines, got %d lines", lines)
	}
	web := shown(t, state, "claude-web", false)
	if strings.Contains(web, "claude-api") || strings.Count(web, "\n") != 30 {
		t.Errorf("only web: %q", web)
	}
	if got := shown(t, state, "claude-none", false); got != "no events for claude-none\n" {
		t.Errorf("%q", got)
	}
	// a name that is only part of another is not a match
	if got := shown(t, state, "claude", false); got != "no events for claude\n" {
		t.Errorf("%q", got)
	}
}

func TestShowFullTranscript(t *testing.T) {
	state := t.TempDir()
	if got := shown(t, state, "claude-web", true); got != "no log for claude-web\n" {
		t.Errorf("%q", got)
	}
	if err := os.MkdirAll(filepath.Join(state, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "logs", "claude-web.log"), []byte("\n===== x up claude-web\nReady: ssh hetzner-claude-web\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := shown(t, state, "claude-web", true); !strings.Contains(got, "Ready: ssh hetzner-claude-web\n") {
		t.Errorf("%q", got)
	}
}
