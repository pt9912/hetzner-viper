package runlog

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Show prints the events of all machines (the last 50 lines) or, with a server, the events of
// that server, or with full its whole transcript (make log, viper log).
func Show(stateDir, server string, full bool, out io.Writer) error {
	events := filepath.Join(stateDir, "events.log")
	switch {
	case server == "":
		b, err := os.ReadFile(events)
		if err != nil {
			fmt.Fprintln(out, "no events yet")
			return nil
		}
		lines := strings.SplitAfter(string(b), "\n")
		if n := len(lines); n > 0 && lines[n-1] == "" {
			lines = lines[:n-1]
		}
		if len(lines) > 50 {
			lines = lines[len(lines)-50:]
		}
		_, err = io.WriteString(out, strings.Join(lines, ""))
		return err
	case full:
		b, err := os.ReadFile(filepath.Join(stateDir, "logs", server+".log"))
		if err != nil {
			fmt.Fprintf(out, "no log for %s\n", server)
			return nil
		}
		_, err = out.Write(b)
		return err
	}
	b, err := os.ReadFile(events)
	shown := false
	if err == nil {
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) >= 2 && f[1] == server {
				fmt.Fprintln(out, sc.Text())
				shown = true
			}
		}
	}
	if !shown {
		fmt.Fprintf(out, "no events for %s\n", server)
	}
	return nil
}
