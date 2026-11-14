package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

// uploadTable holds the files a page handed over to send, in a folder of their own that only this user reaches.
type uploadTable struct {
	mu    sync.Mutex
	dir   string
	files map[string]upload
}

type upload struct {
	path, kind string
	at         time.Time
}

const (
	maxUploads = 8
	uploadLife = 30 * time.Minute
)

var kindRe = regexp.MustCompile(`^(pdf|image|zip)$`)

// cleanFileName keeps what a name of a file may hold: letters, digits and . _ - (the command checks the name again).
func cleanFileName(n string) string {
	n = filepath.Base(strings.ReplaceAll(n, `\`, "/"))
	var b strings.Builder
	for _, r := range n {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._-", r)) {
			b.WriteRune(r)
		}
	}
	out := strings.TrimLeft(b.String(), ".-")
	if len(out) > 64 {
		out = out[len(out)-64:]
	}
	if out == "" {
		return "file"
	}
	return out
}

// POST /api/upload?kind=pdf|image|zip&name=report.pdf, the file as the body.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Readonly || s.cfg.Spawn == nil {
		fail(w, http.StatusForbidden, "this page is read only")
		return
	}
	kind := r.URL.Query().Get("kind")
	if !kindRe.MatchString(kind) {
		fail(w, http.StatusBadRequest, "the kind is pdf, image or zip")
		return
	}
	t := &s.uploads
	t.mu.Lock()
	t.expire()
	if len(t.files) >= maxUploads {
		t.mu.Unlock()
		fail(w, http.StatusServiceUnavailable, "too many files waiting; send one first")
		return
	}
	if t.dir == "" {
		dir, err := os.MkdirTemp("", "viper-ui-up-")
		if err != nil {
			t.mu.Unlock()
			fail(w, http.StatusInternalServerError, "no folder for the file")
			return
		}
		t.dir, t.files = dir, map[string]upload{}
	}
	id := random()
	sub := filepath.Join(t.dir, id)
	t.mu.Unlock()
	path := filepath.Join(sub, cleanFileName(r.URL.Query().Get("name")))
	if err := s.save(w, r, sub, path); err != nil {
		_ = os.RemoveAll(sub)
		return
	}
	t.mu.Lock()
	t.files[id] = upload{path: path, kind: kind, at: s.cfg.Now()}
	t.mu.Unlock()
	reply(w, map[string]any{"id": id})
}

// save writes the body to the file, at most MaxUpload bytes.
func (s *Server) save(w http.ResponseWriter, r *http.Request, sub, path string) error {
	if err := os.Mkdir(sub, 0o700); err != nil {
		fail(w, http.StatusInternalServerError, "no folder for the file")
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fail(w, http.StatusInternalServerError, "no file")
		return err
	}
	_, err = io.Copy(f, http.MaxBytesReader(w, r.Body, s.cfg.MaxUpload))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			fail(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the file has more than %d MiB", s.cfg.MaxUpload>>20))
		} else {
			fail(w, http.StatusBadRequest, "the file did not arrive")
		}
	}
	return err
}

// expire deletes the files nobody sent in time; the caller holds the lock.
func (t *uploadTable) expire() {
	for id, u := range t.files {
		if time.Since(u.at) > uploadLife {
			_ = os.RemoveAll(filepath.Dir(u.path))
			delete(t.files, id)
		}
	}
}

// take hands the file to a run: it is not offered again.
func (t *uploadTable) take(id string) (upload, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	u, ok := t.files[id]
	delete(t.files, id)
	return u, ok
}

func (t *uploadTable) removeAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dir != "" {
		_ = os.RemoveAll(t.dir)
	}
	t.dir, t.files = "", nil
}

// GET /api/download?id=RUN: the file a fetch run put on this computer. It changes nothing; the answer is a download, never
// a page of this server (octet-stream, attachment, nosniff).
func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	run := s.lookupRun(r.URL.Query().Get("id"))
	if run == nil || run.files == nil || run.Action != "nats" || run.current() != stateDone || run.exit() != 0 {
		fail(w, http.StatusNotFound, "no file")
		return
	}
	entries, err := os.ReadDir(filepath.Join(run.files.dir, "out"))
	if err != nil || len(entries) != 1 || !entries[0].Type().IsRegular() {
		fail(w, http.StatusNotFound, "no file")
		return
	}
	f, err := os.Open(filepath.Join(run.files.dir, "out", entries[0].Name()))
	if err != nil {
		fail(w, http.StatusNotFound, "no file")
		return
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(cleanFileName(entries[0].Name())))
	_, _ = io.Copy(w, f)
}
