package natsmcp

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// FileLogger writes to stderr and to a file that is rotated at a size limit. It logs what the
// server does (who, kind, size), never the content of a message.
type FileLogger struct {
	mu   sync.Mutex
	w    io.Writer
	path string
	max  int64
	f    *os.File
}

// NewFileLogger logs to w and, if path is not empty, to path (rotated to path.1 at max bytes).
func NewFileLogger(w io.Writer, path string, limit int64) *FileLogger {
	return &FileLogger{w: w, path: path, max: limit}
}

// Printf writes one line with the time.
func (l *FileLogger) Printf(format string, args ...any) {
	line := time.Now().UTC().Format("2006-01-02T15:04:05Z") + " " + fmt.Sprintf(format, args...) + "\n"
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w != nil {
		_, _ = io.WriteString(l.w, line) // a failing log must not stop the server
	}
	if l.path == "" {
		return
	}
	if l.f == nil {
		f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return
		}
		l.f = f
	}
	if st, err := l.f.Stat(); err == nil && st.Size() > l.max {
		_ = l.f.Close()
		_ = os.Rename(l.path, l.path+".1")
		l.f = nil
		f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return
		}
		l.f = f
	}
	_, _ = io.WriteString(l.f, line)
}

// Close closes the file.
func (l *FileLogger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		_ = l.f.Close()
		l.f = nil
	}
}
