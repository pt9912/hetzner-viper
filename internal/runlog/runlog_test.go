package runlog_test

import (
	"bytes"
	"errors"
	"github.com/pt9912/hetzner-viper/internal/runlog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func open(t *testing.T) (*runlog.Log, string, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	state := filepath.Join(t.TempDir(), "state")
	var out, errb bytes.Buffer
	l, err := runlog.Open(state, "claude-web", "up", &out, &errb)
	if err != nil {
		t.Fatal(err)
	}
	l.SetNow(func() time.Time { return time.Date(2026, 10, 21, 15, 12, 3, 0, time.FixedZone("CEST", 2*3600)) })
	return l, state, &out, &errb
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEventsHaveTheFormatOfTheScripts(t *testing.T) {
	l, state, _, _ := open(t)
	l.Event("start (%s, %s)", "cpx32", "fsn1")
	l.Final("ready")
	got := read(t, filepath.Join(state, "events.log"))
	want := "2026-10-21T15:12:03+02:00 claude-web               up        start (cpx32, fsn1)\n" +
		"2026-10-21T15:12:03+02:00 claude-web               up        ready\n"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}

func TestOutputGoesToTheTerminalAndTheTranscript(t *testing.T) {
	l, state, out, errb := open(t)
	_, _ = l.Out.Write([]byte("hello\n"))
	_, _ = l.Err.Write([]byte("oops\n"))
	l.Close(nil, 0)
	if out.String() != "hello\n" || errb.String() != "oops\n" {
		t.Errorf("terminal %q %q", out, errb)
	}
	log := read(t, filepath.Join(state, "logs", "claude-web.log"))
	if !regexp.MustCompile(`^\n===== \d{4}-\d\d-\d\dT\d\d:\d\d:\d\d[+-]\d\d:\d\d up claude-web\nhello\noops\n$`).MatchString(log) {
		t.Errorf("transcript %q", log)
	}
}

func TestAFailingRunWithoutAFinalEventIsLoggedAsFailed(t *testing.T) {
	l, state, _, _ := open(t)
	l.Close(errors.New("boom"), 1)
	if got := read(t, filepath.Join(state, "events.log")); !strings.HasSuffix(got, "FAILED (exit 1; see logs/claude-web.log)\n") {
		t.Errorf("got %q", got)
	}
	l, state, _, _ = open(t)
	l.Final("preflight: 2 problem(s)")
	l.Close(errors.New("x"), 1)
	if got := read(t, filepath.Join(state, "events.log")); strings.Contains(got, "FAILED") {
		t.Errorf("a final event replaces FAILED: %q", got)
	}
	l, state, _, _ = open(t)
	l.Interrupted()
	l.Close(errors.New("x"), 130)
	if got := read(t, filepath.Join(state, "events.log")); !strings.HasSuffix(got, "interrupted\n") || strings.Contains(got, "FAILED") {
		t.Errorf("got %q", got)
	}
}

func TestPrivateFilesAndDirectories(t *testing.T) {
	l, state, _, _ := open(t)
	l.Event("x")
	for p, want := range map[string]os.FileMode{
		state: 0o700, filepath.Join(state, "logs"): 0o700,
		filepath.Join(state, "logs", "claude-web.log"): 0o600, filepath.Join(state, "events.log"): 0o600,
	} {
		if st, err := os.Stat(p); err != nil || st.Mode().Perm() != want {
			t.Errorf("%s: %v %v", p, st, err)
		}
	}
}
