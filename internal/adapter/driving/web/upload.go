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
	files map[string]*upload
}

type upload struct {
	path, kind string
	ready      bool        // the file has arrived; before that the entry only holds the place
	timer      *time.Timer // deletes a file that nobody sends
}

const maxUploads = 8

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

// POST /api/upload?kind=pdf|image|zip&group=dev&name=report.pdf, the file as the body.
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
	limit, ok := s.uploadLimit(w, r)
	if !ok {
		return
	}
	id, sub, ok := s.reserveUpload(w)
	if !ok {
		return
	}
	path := filepath.Join(sub, cleanFileName(r.URL.Query().Get("name")))
	if err := save(w, r, sub, path, limit); err != nil {
		s.uploads.discard(id)
		return
	}
	t := &s.uploads
	t.mu.Lock()
	if u := t.files[id]; u != nil { // the table may have been emptied meanwhile (the server ends)
		u.path, u.kind, u.ready = path, kind, true
		u.timer = time.AfterFunc(s.cfg.UploadLife, func() { t.discard(id) })
	}
	t.mu.Unlock()
	reply(w, map[string]any{"id": id})
}

// uploadLimit is the largest file for the group of the request: the limit of the group (its object store, which a group
// without NATS_OBJECTS does not have) and the limit of the page.
func (s *Server) uploadLimit(w http.ResponseWriter, r *http.Request) (int64, bool) {
	ov, err := s.snapshot(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, s.show(err.Error()))
		return 0, false
	}
	g, ok := findNATS(ov, r.URL.Query().Get("group"))
	if !ok {
		fail(w, http.StatusBadRequest, "no such NATS group")
		return 0, false
	}
	if g.FileMax <= 0 {
		fail(w, http.StatusConflict, "this group has no object store (NATS_OBJECTS=true): files cannot be sent")
		return 0, false
	}
	return min(g.FileMax, s.cfg.MaxUpload), true
}

// reserveUpload takes a place in the table (before the file is written, so that many uploads at once cannot pass the limit)
// and makes the folder of the file.
func (s *Server) reserveUpload(w http.ResponseWriter) (id, sub string, ok bool) {
	t := &s.uploads
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.files) >= maxUploads {
		fail(w, http.StatusServiceUnavailable, "too many files waiting; send one first")
		return "", "", false
	}
	if t.dir == "" {
		dir, err := os.MkdirTemp("", "viper-ui-up-")
		if err != nil {
			fail(w, http.StatusInternalServerError, "no folder for the file")
			return "", "", false
		}
		t.dir, t.files = dir, map[string]*upload{}
	}
	id = random()
	t.files[id] = &upload{}
	return id, filepath.Join(t.dir, id), true
}

// save writes the body to the file, at most limit bytes.
func save(w http.ResponseWriter, r *http.Request, sub, path string, limit int64) error {
	if err := os.Mkdir(sub, 0o700); err != nil {
		fail(w, http.StatusInternalServerError, "no folder for the file")
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fail(w, http.StatusInternalServerError, "no file")
		return err
	}
	_, err = io.Copy(f, http.MaxBytesReader(w, r.Body, limit))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			fail(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the file has more than %d MiB (NATS_OBJECTS_FILE_MAX of the group)", limit>>20))
		} else {
			fail(w, http.StatusBadRequest, "the file did not arrive")
		}
	}
	return err
}

// take hands the file to a run: it is not offered again.
func (t *uploadTable) take(id string) (upload, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	u := t.files[id]
	if u == nil || !u.ready {
		return upload{}, false
	}
	delete(t.files, id)
	if u.timer != nil {
		u.timer.Stop()
	}
	return *u, true
}

// discard deletes a file that was handed over and is not used (or whose upload failed).
func (t *uploadTable) discard(id string) {
	t.mu.Lock()
	u := t.files[id]
	delete(t.files, id)
	dir := t.dir
	t.mu.Unlock()
	if u == nil {
		return
	}
	if u.timer != nil {
		u.timer.Stop()
	}
	if dir != "" {
		_ = os.RemoveAll(filepath.Join(dir, id))
	}
}

func (t *uploadTable) removeAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, u := range t.files {
		if u.timer != nil {
			u.timer.Stop()
		}
	}
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
