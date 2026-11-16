// Package terminal opens a terminal window of the desktop that runs a program (viper terminal): the adapter behind the
// button "Terminal" of the pages. The words of the program go to the terminal program one by one, never as a line of a shell,
// except on macOS, where the Terminal takes a file.
package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pt9912/hetzner-viper/internal/model"
)

// programs are the terminal programs of Linux in the order they are tried, with the words that come before the program to run.
// x-terminal-emulator is not among them: its -e takes only one line of a shell for some of the programs it stands for.
func programs() []program {
	return []program{
		{"gnome-terminal", []string{"--"}},
		{"konsole", []string{"-e"}},
		{"xfce4-terminal", []string{"-x"}},
		{"kitty", nil},
		{"alacritty", []string{"-e"}},
		{"wezterm", []string{"start", "--"}},
		{"xterm", []string{"-e"}},
	}
}

type program struct {
	name   string
	before []string
}

// Names are the terminal programs VIPER_TERMINAL may name.
func Names() []string {
	var out []string
	for _, p := range programs() {
		out = append(out, p.name)
	}
	return out
}

// Plan is the program and the arguments that open a window that runs the words, for Linux and Windows. getenv reads the
// environment, lookPath finds a program. The error says what is missing, in a form that is shown to the person.
func Plan(goos string, getenv func(string) string, lookPath func(string) (string, error), words []string) (string, []string, error) {
	if len(words) == 0 {
		return "", nil, errors.New("nothing to run")
	}
	switch goos {
	case "windows":
		return planWindows(lookPath, words)
	case "linux", "freebsd", "openbsd", "netbsd":
		return planUnix(getenv, lookPath, words)
	}
	return "", nil, fmt.Errorf("a terminal window is not supported on %s", goos)
}

func planWindows(lookPath func(string) (string, error), words []string) (string, []string, error) {
	for _, w := range words {
		if strings.Contains(w, ";") {
			return "", nil, fmt.Errorf("Windows Terminal reads ; as a separator: %q cannot be passed", w)
		}
	}
	path, err := lookPath("wt.exe")
	if err != nil {
		return "", nil, errors.New("Windows Terminal not found: install it, or run viper ssh --tmux in a terminal yourself")
	}
	return path, append([]string{"new-tab"}, words...), nil
}

func planUnix(getenv func(string) string, lookPath func(string) (string, error), words []string) (string, []string, error) {
	if getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
		return "", nil, errors.New("no desktop: viper ui runs without DISPLAY or WAYLAND_DISPLAY, so there is no window to open")
	}
	want := getenv("VIPER_TERMINAL")
	known := false
	for _, p := range programs() {
		if want != "" && p.name != want {
			continue
		}
		known = true
		if path, err := lookPath(p.name); err == nil {
			return path, append(append([]string(nil), p.before...), words...), nil
		}
	}
	if want != "" && !known {
		return "", nil, fmt.Errorf("VIPER_TERMINAL=%s: not one of %s", want, strings.Join(Names(), ", "))
	}
	tried := strings.Join(Names(), ", ")
	if want != "" {
		return "", nil, fmt.Errorf("%s (VIPER_TERMINAL) is not installed: no terminal program found; install it or unset VIPER_TERMINAL", want)
	}
	return "", nil, fmt.Errorf("no terminal program found (tried %s): install one, or set VIPER_TERMINAL to one of them", tried)
}

// quote is a word in single quotes for sh (a ' becomes '\”).
func quote(w string) string { return "'" + strings.ReplaceAll(w, "'", `'\''`) + "'" }

// MacScript is the file that the Terminal of macOS opens: it runs the words (every word in single quotes).
func MacScript(words []string) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = quote(w)
	}
	return "#!/bin/sh\nexec " + strings.Join(q, " ") + "\n"
}

// Launcher opens windows. Env is the environment of the terminal program (without credentials), Getenv reads the environment of
// viper (VIPER_TERMINAL, DISPLAY).
type Launcher struct {
	GOOS     string
	Env      []string
	Getenv   func(string) string
	LookPath func(string) (string, error)
}

// Launch opens a window that runs the words. It does not wait for the window: it waits one second to see whether the program
// ends at once with an error, and says so (a window that stays open is the usual case). The window is in a session of its own
// and outlives viper.
func (l Launcher) Launch(words []string) error {
	name, args, err := l.plan(words)
	if err != nil {
		return err
	}
	errFile, err := os.CreateTemp("", "viper-terminal-")
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(context.Background(), name, args...) //nolint:gosec // the program is one of a fixed list, the words are made by viper terminal
	cmd.Env = l.Env
	cmd.Stderr = errFile
	cmd.SysProcAttr = detached()
	if err := cmd.Start(); err != nil {
		cleanup(errFile)
		return err
	}
	if runtime.GOOS != "windows" {
		_ = os.Remove(errFile.Name()) // the child holds it: nothing is left behind when viper ends before the window does
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case e := <-done:
		defer cleanup(errFile)
		if e != nil {
			// %v, not %w: a wrapped *exec.ExitError would be taken by viper for the exit status of a program it ran, and not shown
			return fmt.Errorf("%s: %v%s", filepath.Base(name), e, firstLines(errFile))
		}
		return nil // some programs (gnome-terminal, konsole, open) hand the window over and end at once
	case <-time.After(time.Second):
		go func() { e := <-done; _ = e; cleanup(errFile) }()
		return nil
	}
}

// cleanup closes the file of the error output and removes it (where it is still there).
func cleanup(f *os.File) {
	_ = f.Close()
	_ = os.Remove(f.Name())
}

func (l Launcher) plan(words []string) (string, []string, error) {
	if l.GOOS != "darwin" {
		return Plan(l.GOOS, l.Getenv, l.LookPath, words)
	}
	path, err := macFile(words)
	if err != nil {
		return "", nil, err
	}
	open, err := l.LookPath("open")
	if err != nil {
		return "", nil, errors.New("open not found")
	}
	return open, []string{"-a", "Terminal", path}, nil
}

// macFile writes the script for the Terminal into a folder of the state directory (0700) under a name that nobody chose, and
// removes the older ones: open returns before the Terminal has read the file, so a file is never removed at once.
func macFile(words []string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(model.StateDir(home), "terminal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil { // a folder that was there before keeps its rights otherwise
		return "", err
	}
	if old, _ := filepath.Glob(filepath.Join(dir, "*.command")); old != nil {
		for _, f := range old {
			if st, err := os.Stat(f); err == nil && time.Since(st.ModTime()) > 24*time.Hour {
				_ = os.Remove(f)
			}
		}
	}
	f, err := os.CreateTemp(dir, "*.command")
	if err != nil {
		return "", err
	}
	fail := func(err error) (string, error) { _ = f.Close(); _ = os.Remove(f.Name()); return "", err }
	if err := f.Chmod(0o700); err != nil {
		return fail(err)
	}
	if _, err := io.WriteString(f, MacScript(words)); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// firstLines is what the program wrote to its error output, a few lines, for the message.
func firstLines(f *os.File) string {
	b := make([]byte, 400)
	n, _ := f.ReadAt(b, 0)
	if n == 0 {
		return ""
	}
	return ": " + strings.TrimSpace(string(b[:n]))
}
