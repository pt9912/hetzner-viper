package cli

import (
	"bytes"
	"io"
	"sync"
)

// prefixWriter writes what one machine of a group says, line by line, each with the name of the
// machine in front: the machines run at the same time and share the terminal. A line is written
// whole and under the lock of the terminal, so lines of two machines never mix; the rest of an
// unfinished line waits for its end (or for flush). A carriage return ends a line as well, so a
// progress display shows up as lines.
type prefixWriter struct {
	mu     *sync.Mutex // the terminal's: one for all machines (and both streams)
	out    io.Writer
	prefix string
	rmu    sync.Mutex // the unfinished line: a program that outlives its run may still write
	rest   []byte
}

func newPrefixWriter(mu *sync.Mutex, out io.Writer, machine string, width int) *prefixWriter {
	pad := ""
	for i := len(machine); i < width; i++ {
		pad += " "
	}
	return &prefixWriter{mu: mu, out: out, prefix: "[" + machine + "]" + pad + " "}
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	w.rmu.Lock()
	defer w.rmu.Unlock()
	w.rest = append(w.rest, p...)
	for {
		i := bytes.IndexAny(w.rest, "\n\r")
		if i < 0 {
			return len(p), nil
		}
		line := w.rest[:i]
		next := i + 1
		if w.rest[i] == '\r' && next < len(w.rest) && w.rest[next] == '\n' {
			next++
		}
		w.emit(line)
		w.rest = w.rest[next:]
	}
}

func (w *prefixWriter) emit(line []byte) {
	if len(line) == 0 {
		return // empty lines would only space the machines out
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = io.WriteString(w.out, w.prefix+string(line)+"\n") // a terminal that cannot be written to has no one to tell
}

// flush writes an unfinished last line.
func (w *prefixWriter) flush() {
	w.rmu.Lock()
	defer w.rmu.Unlock()
	if len(w.rest) > 0 {
		w.emit(w.rest)
		w.rest = nil
	}
}
